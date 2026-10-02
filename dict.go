// Package masker 提供流式敏感词遮罩：词典加载、按消息创建处理过程、
// 分段提交字节片段并增量返回遮罩结果。
package masker

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

// edge 是自动机的一条转移边。
type edge struct {
	r  rune
	to int32
}

// Dictionary 是加载完成后固定不变的敏感词词典（Aho-Corasick 自动机）。
// 转移边按码点排序存储（CSR 格式），运行时二分查找。
type Dictionary struct {
	maxLen   int32   // 最长词的码点数
	edgeFrom []int32 // 每个节点的边在 edges 中的起始下标，长度为节点数+1
	edges    []edge  // 全部边，按节点分组、组内按码点升序
	fail     []int32 // 每个节点的失配指针
	maxMatch []int32 // 以该节点结尾的最长词长度（码点数，含失配链上的词），0 表示无
}

// LoadDictionary 从本地 UTF-8 文件加载词典，每行一个词。
// 空行忽略；词典必须非空，词条必须非空。加载后词典固定不变。
func LoadDictionary(path string) (*Dictionary, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var words []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		w := strings.TrimRight(sc.Text(), "\r")
		if w != "" {
			words = append(words, w)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return NewDictionary(words)
}

// NewDictionary 用词表构建词典。词表必须非空且每个词非空。
func NewDictionary(words []string) (*Dictionary, error) {
	if len(words) == 0 {
		return nil, fmt.Errorf("masker: empty dictionary")
	}
	for _, w := range words {
		if w == "" {
			return nil, fmt.Errorf("masker: empty word")
		}
	}
	d := &Dictionary{}
	d.build(words)
	return d, nil
}

// MaxWordLen 返回最长词的码点数。
func (d *Dictionary) MaxWordLen() int { return int(d.maxLen) }

// nodeCount 返回自动机节点数（仅供测试/基准使用）。
func (d *Dictionary) nodeCount() int { return len(d.fail) }

// build 构建 Aho-Corasick 自动机。
func (d *Dictionary) build(words []string) {
	type tmpNode struct {
		children map[rune]int32
		out      int32 // 以该节点结尾的词长（码点数）
	}
	nodes := []tmpNode{{}}
	for _, w := range words {
		runes := []rune(w)
		if int32(len(runes)) > d.maxLen {
			d.maxLen = int32(len(runes))
		}
		cur := int32(0)
		for _, r := range runes {
			next, ok := nodes[cur].children[r]
			if !ok {
				next = int32(len(nodes))
				nodes = append(nodes, tmpNode{})
				if nodes[cur].children == nil {
					nodes[cur].children = make(map[rune]int32)
				}
				nodes[cur].children[r] = next
			}
			cur = next
		}
		nodes[cur].out = int32(len(runes))
	}

	n := len(nodes)
	d.fail = make([]int32, n)
	d.maxMatch = make([]int32, n)

	// BFS 计算失配指针，并按 BFS 顺序把失配链上的最大词长合并到 maxMatch。
	queue := make([]int32, 0, n)
	for _, c := range nodes[0].children {
		queue = append(queue, c)
	}
	for head := 0; head < len(queue); head++ {
		u := queue[head]
		for r, v := range nodes[u].children {
			queue = append(queue, v)
			f := d.fail[u]
			for f != 0 {
				if t, ok := nodes[f].children[r]; ok {
					d.fail[v] = t
					break
				}
				f = d.fail[f]
			}
			if t, ok := nodes[f].children[r]; f == 0 && ok && t != v {
				d.fail[v] = t
			}
		}
		d.maxMatch[u] = nodes[u].out
		if fm := d.maxMatch[d.fail[u]]; fm > d.maxMatch[u] {
			d.maxMatch[u] = fm
		}
	}
	// 根节点本身。
	d.maxMatch[0] = nodes[0].out

	// 转为 CSR 存储。
	total := 0
	for i := range nodes {
		total += len(nodes[i].children)
	}
	d.edgeFrom = make([]int32, n+1)
	d.edges = make([]edge, total)
	off := int32(0)
	for i := range nodes {
		d.edgeFrom[i] = off
		ch := nodes[i].children
		rs := make([]rune, 0, len(ch))
		for r := range ch {
			rs = append(rs, r)
		}
		sort.Slice(rs, func(a, b int) bool { return rs[a] < rs[b] })
		for _, r := range rs {
			d.edges[off] = edge{r: r, to: ch[r]}
			off++
		}
		nodes[i].children = nil
	}
	d.edgeFrom[n] = off
}

// step 从状态 state 消费一个码点，返回新状态。
func (d *Dictionary) step(state int32, r rune) int32 {
	for {
		lo, hi := d.edgeFrom[state], d.edgeFrom[state+1]
		i := sort.Search(int(hi-lo), func(i int) bool { return d.edges[int(lo)+i].r >= r })
		if int(lo)+i < int(hi) && d.edges[int(lo)+i].r == r {
			return d.edges[int(lo)+i].to
		}
		if state == 0 {
			return 0
		}
		state = d.fail[state]
	}
}

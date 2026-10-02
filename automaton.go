package masker

import "sort"

// automaton 是基于 Aho-Corasick 的确定性自动机，
// 以 Unicode 码点为转移单位，按 CSR 扁平存储。
type automaton struct {
	// 每个节点的出边在 edges 中的区间 [edgeOff[i], edgeOff[i+1])
	edgeOff []uint32
	// 出边按码点升序排列，与 edgeTo 一一对应
	edgeRune []rune
	edgeTo   []uint32
	// fail[i] 为节点 i 的失败指针
	fail []uint32
	// maxMatch[i] 为在节点 i 处结束的最长词典词的码点长度，0 表示无。
	// 同一位置结束的多个词互为后缀，最长者覆盖其余，故只需保留最长值。
	maxMatch []uint32
}

const smallEdgeLimit = 16

// trieBuilder 构建期使用的临时 trie，之后压缩为 CSR。
type trieBuilder struct {
	small [][]trieEdge      // 度数 < smallEdgeLimit 时的出边
	big   []map[rune]uint32 // 度数达到 smallEdgeLimit 后晋升的出边表
	word  []uint32          // 恰好终止于该节点的词的码点长度，0 表示无
}

type trieEdge struct {
	r  rune
	to uint32
}

func newTrieBuilder() *trieBuilder {
	b := &trieBuilder{}
	b.addNode()
	return b
}

func (b *trieBuilder) addNode() uint32 {
	b.small = append(b.small, nil)
	b.big = append(b.big, nil)
	b.word = append(b.word, 0)
	return uint32(len(b.small) - 1)
}

func (b *trieBuilder) child(node uint32, r rune) (uint32, bool) {
	if m := b.big[node]; m != nil {
		to, ok := m[r]
		return to, ok
	}
	for _, e := range b.small[node] {
		if e.r == r {
			return e.to, true
		}
	}
	return 0, false
}

func (b *trieBuilder) addChild(node uint32, r rune) uint32 {
	to := b.addNode()
	if m := b.big[node]; m != nil {
		m[r] = to
		return to
	}
	b.small[node] = append(b.small[node], trieEdge{r: r, to: to})
	if len(b.small[node]) >= smallEdgeLimit {
		m := make(map[rune]uint32, len(b.small[node])*2)
		for _, e := range b.small[node] {
			m[e.r] = e.to
		}
		b.big[node] = m
		b.small[node] = nil
	}
	return to
}

func (b *trieBuilder) insert(word string) {
	runes := []rune(word)
	node := uint32(0)
	for _, r := range runes {
		next, ok := b.child(node, r)
		if !ok {
			next = b.addChild(node, r)
		}
		node = next
	}
	b.word[node] = uint32(len(runes))
}

// buildAutomaton 由词条集合构建自动机。words 非空且每个词非空。
func buildAutomaton(words []string) *automaton {
	b := newTrieBuilder()
	for _, w := range words {
		b.insert(w)
	}
	n := len(b.word)

	a := &automaton{
		edgeOff:  make([]uint32, n+1),
		fail:     make([]uint32, n),
		maxMatch: make([]uint32, n),
	}
	total := 0
	for i := 0; i < n; i++ {
		a.edgeOff[i] = uint32(total)
		if b.big[i] != nil {
			total += len(b.big[i])
		} else {
			total += len(b.small[i])
		}
	}
	a.edgeOff[n] = uint32(total)
	a.edgeRune = make([]rune, total)
	a.edgeTo = make([]uint32, total)
	for i := 0; i < n; i++ {
		off := a.edgeOff[i]
		if b.big[i] != nil {
			rs := make([]rune, 0, len(b.big[i]))
			for r := range b.big[i] {
				rs = append(rs, r)
			}
			sort.Slice(rs, func(x, y int) bool { return rs[x] < rs[y] })
			for _, r := range rs {
				a.edgeRune[off] = r
				a.edgeTo[off] = b.big[i][r]
				off++
			}
		} else {
			es := b.small[i]
			sort.Slice(es, func(x, y int) bool { return es[x].r < es[y].r })
			for _, e := range es {
				a.edgeRune[off] = e.r
				a.edgeTo[off] = e.to
				off++
			}
		}
	}

	// BFS 计算失败指针。maxMatch 在节点出队时合并：
	// 此时 fail[node] 深度更小、已出队，其 maxMatch 已最终确定。
	queue := make([]uint32, 0, n)
	for off := a.edgeOff[0]; off < a.edgeOff[1]; off++ {
		queue = append(queue, a.edgeTo[off])
	}
	for head := 0; head < len(queue); head++ {
		node := queue[head]
		if b.word[node] > a.maxMatch[node] {
			a.maxMatch[node] = b.word[node]
		}
		if m := a.maxMatch[a.fail[node]]; m > a.maxMatch[node] {
			a.maxMatch[node] = m
		}
		for off := a.edgeOff[node]; off < a.edgeOff[node+1]; off++ {
			r := a.edgeRune[off]
			to := a.edgeTo[off]
			queue = append(queue, to)
			f := a.fail[node]
			for f != 0 {
				if _, ok := a.trans(f, r); ok {
					break
				}
				f = a.fail[f]
			}
			if next, ok := a.trans(f, r); ok && next != to {
				a.fail[to] = next
			} else {
				a.fail[to] = 0
			}
		}
	}
	// 根节点自身的 maxMatch 恒为 0（词非空）。
	return a
}

// trans 仅沿出边转移，不走失败指针。
func (a *automaton) trans(state uint32, r rune) (uint32, bool) {
	lo, hi := a.edgeOff[state], a.edgeOff[state+1]
	for lo < hi {
		mid := (lo + hi) / 2
		if a.edgeRune[mid] < r {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < a.edgeOff[state+1] && a.edgeRune[lo] == r {
		return a.edgeTo[lo], true
	}
	return 0, false
}

// step 从状态 state 消费一个码点，返回新状态及该位置结束的最长命中长度。
func (a *automaton) step(state uint32, r rune) (next uint32, matchLen uint32) {
	for state != 0 {
		if to, ok := a.trans(state, r); ok {
			return to, a.maxMatch[to]
		}
		state = a.fail[state]
	}
	if to, ok := a.trans(0, r); ok {
		return to, a.maxMatch[to]
	}
	return 0, 0
}

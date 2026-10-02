package masker

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// Dictionary 是加载后固定不变的敏感词词典。
type Dictionary struct {
	ac         *automaton
	maxWordLen int // 最长词的码点数
}

// LoadDictionary 从本地 UTF-8 文件加载词典，每行一个词。
// 空行被忽略；词典至少包含一个词。加载后词典固定不变。
func LoadDictionary(path string) (*Dictionary, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("masker: open dictionary: %w", err)
	}
	defer f.Close()

	var words []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if line == "" {
			continue
		}
		words = append(words, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("masker: read dictionary: %w", err)
	}
	return NewDictionary(words)
}

// NewDictionary 直接由词条切片创建词典（便于测试与嵌入）。
// 词典必须非空，且每个词都是非空的合法 UTF-8 字符串。
func NewDictionary(words []string) (*Dictionary, error) {
	if len(words) == 0 {
		return nil, errors.New("masker: dictionary must not be empty")
	}
	maxLen := 0
	for _, w := range words {
		if w == "" {
			return nil, errors.New("masker: dictionary word must not be empty")
		}
		if !utf8.ValidString(w) {
			return nil, fmt.Errorf("masker: dictionary word %q is not valid UTF-8", w)
		}
		if n := utf8.RuneCountInString(w); n > maxLen {
			maxLen = n
		}
	}
	return &Dictionary{ac: buildAutomaton(words), maxWordLen: maxLen}, nil
}

// NewSession 创建一条独立消息的流式遮罩处理过程。
func (d *Dictionary) NewSession() *Session {
	return newSession(d)
}

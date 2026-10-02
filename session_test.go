package masker

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// referenceMask 是独立的一次性完整匹配基准：逐码点暴力比对所有词。
func referenceMask(text string, words []string) string {
	runes := []rune(text)
	covered := make([]bool, len(runes))
	wordRunes := make([][]rune, len(words))
	for i, w := range words {
		wordRunes[i] = []rune(w)
	}
	for i := range runes {
		for _, wr := range wordRunes {
			if i+len(wr) > len(runes) {
				continue
			}
			match := true
			for j, r := range wr {
				if runes[i+j] != r {
					match = false
					break
				}
			}
			if match {
				for j := range wr {
					covered[i+j] = true
				}
			}
		}
	}
	var sb strings.Builder
	for i, r := range runes {
		if covered[i] {
			sb.WriteByte('*')
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// runChunked 按给定分段流式处理，并校验输出延迟上界。
func runChunked(t *testing.T, d *Dictionary, chunks [][]byte) []byte {
	t.Helper()
	s := d.NewSession()
	var out []byte
	var received []byte // 已收到的全部字节
	for _, c := range chunks {
		res := s.Submit(c)
		received = append(received, c...)
		out = append(out, res...)
		pending := completeRuneCount(received) - utf8.RuneCount(out)
		if pending > d.maxWordLen-1 {
			t.Fatalf("pending runes %d exceed maxWordLen-1 %d", pending, d.maxWordLen-1)
		}
	}
	res := s.Finish()
	out = append(out, res...)
	return out
}

// completeRuneCount 统计字节串中完整码点的数量，末尾不完整序列不计入。
func completeRuneCount(b []byte) int {
	n := 0
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 && !utf8.FullRune(b) {
			break
		}
		n++
		b = b[size:]
	}
	return n
}

// allChunkings 对同一消息用多种分段方式验证输出一致。
func checkChunkings(t *testing.T, d *Dictionary, msg string) {
	t.Helper()
	want := runChunked(t, d, [][]byte{[]byte(msg)})

	// 逐字节分段：所有多字节码点都被拆开。
	var byteChunks [][]byte
	for i := 0; i < len(msg); i++ {
		byteChunks = append(byteChunks, []byte(msg[i:i+1]))
	}
	if got := runChunked(t, d, byteChunks); !bytes.Equal(got, want) {
		t.Fatalf("byte-wise chunking: got %q, want %q", got, want)
	}

	// 随机分段（含空片段）。
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		var chunks [][]byte
		rest := []byte(msg)
		for len(rest) > 0 {
			n := rng.Intn(len(rest) + 1)
			chunks = append(chunks, rest[:n])
			rest = rest[n:]
			if rng.Intn(4) == 0 {
				chunks = append(chunks, nil) // 空片段
			}
		}
		if got := runChunked(t, d, chunks); !bytes.Equal(got, want) {
			t.Fatalf("random chunking %d: got %q, want %q", trial, got, want)
		}
	}
}

func newDict(t *testing.T, words ...string) *Dictionary {
	t.Helper()
	d, err := NewDictionary(words)
	if err != nil {
		t.Fatalf("NewDictionary: %v", err)
	}
	return d
}

func TestOverlappingWords(t *testing.T) {
	d := newDict(t, "aba", "bab")
	checkChunkings(t, d, "ababa")
	got := runChunked(t, d, [][]byte{[]byte("ababa")})
	if string(got) != "*****" {
		t.Fatalf("got %q, want %q", got, "*****")
	}
}

func TestAgainstReference(t *testing.T) {
	words := []string{"aba", "bab", "中文", "字正腔圆", "🙂🙃", "a", "aa", "aaa", "Ab", "正"}
	d := newDict(t, words...)
	msgs := []string{
		"ababa",
		"aaaaaa",
		"中文字正腔圆中文",
		"🙂🙃🙂🙃x🙂",
		"Ab aB ab AB",
		"混合mix🙂中文aba正腔圆",
		"无命中内容 nothing here",
		"",
		"a",
	}
	for _, msg := range msgs {
		want := referenceMask(msg, words)
		got := runChunked(t, d, [][]byte{[]byte(msg)})
		if string(got) != want {
			t.Fatalf("msg %q: got %q, reference %q", msg, got, want)
		}
		checkChunkings(t, d, msg)
	}
}

func TestLongCandidatePrefixMismatch(t *testing.T) {
	// 词为 1024 个 a 加 b；输入全为 a，永远差最后一个字符。
	word := strings.Repeat("a", 1024) + "b"
	d := newDict(t, word)
	msg := strings.Repeat("a", 4096)
	got := runChunked(t, d, [][]byte{[]byte(msg)})
	if string(got) != msg {
		t.Fatalf("false mask: got %d stars", strings.Count(string(got), "*"))
	}
	checkChunkings(t, d, msg)

	// 前缀失配后紧跟完整命中。
	d2 := newDict(t, word, "aa")
	msg2 := strings.Repeat("a", 2048)
	want2 := referenceMask(msg2, []string{word, "aa"})
	got2 := runChunked(t, d2, [][]byte{[]byte(msg2)})
	if string(got) != msg || string(got2) != want2 {
		t.Fatalf("got2 stars=%d, want %d", strings.Count(string(got2), "*"), strings.Count(want2, "*"))
	}
}

func TestSessionIsolation(t *testing.T) {
	d := newDict(t, "hello")
	s1 := d.NewSession()
	s2 := d.NewSession()
	o1 := string(s1.Submit([]byte("hel")))
	o2 := string(s2.Submit([]byte("xxhello")))
	o1 += string(s1.Submit([]byte("lo")))
	o1 += string(s1.Finish())
	o2 += string(s2.Finish())
	if o1 != "*****" || o2 != "xx*****" {
		t.Fatalf("o1=%q o2=%q", o1, o2)
	}
}

func TestLoadDictionaryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dict.txt")
	content := "aba\n中文\n\n🙂🙃\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := LoadDictionary(path)
	if err != nil {
		t.Fatalf("LoadDictionary: %v", err)
	}
	checkChunkings(t, d, "aba中文🙂🙃")

	if _, err := LoadDictionary(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatal("expected error for missing file")
	}
	empty := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(empty, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDictionary(empty); err == nil {
		t.Fatal("expected error for empty dictionary")
	}
	if _, err := NewDictionary([]string{"ok", ""}); err == nil {
		t.Fatal("expected error for empty word")
	}
}

func TestEmptyChunks(t *testing.T) {
	d := newDict(t, "abc")
	s := d.NewSession()
	var out []byte
	out = append(out, s.Submit(nil)...)
	out = append(out, s.Submit([]byte(""))...)
	out = append(out, s.Submit([]byte("abc"))...)
	out = append(out, s.Submit(nil)...)
	out = append(out, s.Finish()...)
	if string(out) != "***" {
		t.Fatalf("got %q", out)
	}
}

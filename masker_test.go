package masker

import (
	"bytes"
	"math/rand"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// referenceMask 是独立的一次性完整匹配基准：对每个词在原文中找所有
// 出现位置（含重叠），被覆盖的码点替换为 '*'。不依赖库内部实现。
func referenceMask(words []string, text []byte) []byte {
	runes := []rune(string(text))
	// 每个码点的原始字节切片。
	type span struct{ start, end int }
	spans := make([]span, 0, len(runes))
	masked := make([]bool, 0, len(runes))
	for i := 0; i < len(text); {
		_, size := utf8.DecodeRune(text[i:])
		spans = append(spans, span{i, i + size})
		masked = append(masked, false)
		i += size
	}
	for _, w := range words {
		wr := []rune(w)
		if len(wr) == 0 || len(wr) > len(runes) {
			continue
		}
		for i := 0; i+len(wr) <= len(runes); i++ {
			match := true
			for j := range wr {
				if runes[i+j] != wr[j] {
					match = false
					break
				}
			}
			if match {
				for j := range wr {
					masked[i+j] = true
				}
			}
		}
	}
	var out bytes.Buffer
	for i, sp := range spans {
		if masked[i] {
			out.WriteByte('*')
		} else {
			out.Write(text[sp.start:sp.end])
		}
	}
	return out.Bytes()
}

// feedChunks 按给定切分方式分段提交，返回拼接后的完整输出。
func feedChunks(t *testing.T, d *Dictionary, text []byte, chunkSize int) []byte {
	t.Helper()
	m := d.NewMessage()
	var out []byte
	for i := 0; i < len(text); i += chunkSize {
		end := i + chunkSize
		if end > len(text) {
			end = len(text)
		}
		out = append(out, m.Feed(text[i:end])...)
	}
	out = append(out, m.End()...)
	return out
}

func mustDict(t *testing.T, words ...string) *Dictionary {
	t.Helper()
	d, err := NewDictionary(words)
	if err != nil {
		t.Fatalf("NewDictionary: %v", err)
	}
	return d
}

func TestOverlappingWords(t *testing.T) {
	d := mustDict(t, "aba", "bab")
	got := feedChunks(t, d, []byte("ababa"), 1)
	if string(got) != "*****" {
		t.Fatalf("got %q, want %q", got, "*****")
	}
}

func TestSelfOverlap(t *testing.T) {
	d := mustDict(t, "aa")
	got := feedChunks(t, d, []byte("aaaa"), 3)
	if string(got) != "****" {
		t.Fatalf("got %q, want %q", got, "****")
	}
}

func TestContainedWords(t *testing.T) {
	d := mustDict(t, "he", "she", "her", "hers")
	text := "ushers and hers"
	got := feedChunks(t, d, []byte(text), 2)
	want := referenceMask([]string{"he", "she", "her", "hers"}, []byte(text))
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if string(got) != "u***** and ****" {
		t.Fatalf("got %q", got)
	}
}

func TestCaseSensitive(t *testing.T) {
	d := mustDict(t, "Ab")
	got := feedChunks(t, d, []byte("ab Ab aB AB"), 1)
	if string(got) != "ab ** aB AB" {
		t.Fatalf("got %q", got)
	}
}

func TestEmojiAndMultibyte(t *testing.T) {
	words := []string{"🙂🙃", "秘密", "🙂密"}
	text := "你好🙂🙃呀，这是秘密🙂密！🙂"
	d := mustDict(t, words...)
	want := referenceMask(words, []byte(text))
	// 逐字节提交，必然切在多字节码点内部。
	got := feedChunks(t, d, []byte(text), 1)
	if !bytes.Equal(got, want) {
		t.Fatalf("byte-by-byte: got %q, want %q", got, want)
	}
	// 整体提交结果一致。
	got = feedChunks(t, d, []byte(text), len(text))
	if !bytes.Equal(got, want) {
		t.Fatalf("whole: got %q, want %q", got, want)
	}
}

func TestEmptyChunks(t *testing.T) {
	d := mustDict(t, "ab")
	m := d.NewMessage()
	var out []byte
	out = append(out, m.Feed(nil)...)
	out = append(out, m.Feed([]byte{})...)
	out = append(out, m.Feed([]byte("a"))...)
	out = append(out, m.Feed(nil)...)
	out = append(out, m.Feed([]byte("b"))...)
	out = append(out, m.End()...)
	if string(out) != "**" {
		t.Fatalf("got %q", out)
	}
}

func TestLongPrefixNoMatch(t *testing.T) {
	// 1023 个 a 加 b 组成的词；输入是大量 a 后接 c，最终失配。
	word := strings.Repeat("a", 1023) + "b"
	d := mustDict(t, word)
	text := strings.Repeat("a", 100000) + "c"
	m := d.NewMessage()
	var out []byte
	for i := 0; i < len(text); i += 777 {
		end := i + 777
		if end > len(text) {
			end = len(text)
		}
		out = append(out, m.Feed([]byte(text[i:end]))...)
	}
	out = append(out, m.End()...)
	if string(out) != text {
		t.Fatalf("output was masked or corrupted, len=%d", len(out))
	}
}

func TestLongPrefixMatchAtEnd(t *testing.T) {
	word := strings.Repeat("a", 1023) + "b"
	d := mustDict(t, word)
	text := "xx" + word + "yy"
	got := feedChunks(t, d, []byte(text), 5)
	want := "xx" + strings.Repeat("*", 1024) + "yy"
	if string(got) != want {
		t.Fatalf("got len=%d, want len=%d", len(got), len(want))
	}
}

func TestChunkingInvariance(t *testing.T) {
	words := []string{"aba", "bab", "🙂x", "机密", "a"}
	text := "ab🙂ab🙂xaba机密ba🙂xbab"
	d := mustDict(t, words...)
	want := referenceMask(words, []byte(text))
	for _, size := range []int{1, 2, 3, 4, 5, 7, 11, 64, len(text)} {
		got := feedChunks(t, d, []byte(text), size)
		if !bytes.Equal(got, want) {
			t.Fatalf("chunk=%d: got %q, want %q", size, got, want)
		}
	}
}

func TestPendingLimit(t *testing.T) {
	words := []string{strings.Repeat("a", 100) + "z", "ab"}
	d := mustDict(t, words...)
	maxLen := d.MaxWordLen() // 101
	rng := rand.New(rand.NewSource(42))
	text := []byte(strings.Repeat("ab\U0001f642", 5000))
	m := d.NewMessage()
	var outAll []byte
	for fed := 0; fed < len(text); {
		n := 1 + rng.Intn(50)
		if fed+n > len(text) {
			n = len(text) - fed
		}
		outAll = append(outAll, m.Feed(text[fed:fed+n])...)
		fed += n
		// 已收到的完整码点数："末尾尚不完整的 UTF-8 码点不计入"。
		s := text[:fed]
		if len(s) > 0 {
			i := len(s) - 1
			for i > 0 && !_utf8RuneStart(s[i]) {
				i--
			}
			if !utf8.FullRune(s[i:]) {
				s = s[:i] // 截掉末尾残缺码点
			}
		}
		complete := utf8.RuneCount(s)
		pending := complete - utf8.RuneCount(outAll)
		if pending > maxLen-1 {
			t.Fatalf("fed=%d: pending runes %d > maxLen-1 %d", fed, pending, maxLen-1)
		}
		if pending < 0 {
			t.Fatalf("fed=%d: negative pending %d", fed, pending)
		}
	}
	outAll = append(outAll, m.End()...)
	want := referenceMask(words, text)
	if !bytes.Equal(outAll, want) {
		t.Fatalf("output mismatch vs reference")
	}
}

func _utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

func countRunes(b []byte) int { return utf8.RuneCount(b) }

func TestMessagesIndependent(t *testing.T) {
	d := mustDict(t, "ab")
	m1 := d.NewMessage()
	m2 := d.NewMessage()
	o1 := m1.Feed([]byte("xa"))
	o2 := m2.Feed([]byte("b")) // m2 自己的 b 不构成 ab
	e1 := m1.End()
	e2 := m2.End()
	if string(o1)+string(e1) != "xa" {
		t.Fatalf("m1 got %q", string(o1)+string(e1))
	}
	if string(o2)+string(e2) != "b" {
		t.Fatalf("m2 got %q", string(o2)+string(e2))
	}
}

// TestFuzzAgainstReference 用随机词典和随机文本（含多字节字符、随机分段）
// 与独立基准逐字节对比。
func TestFuzzAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []rune("ab🙂🙃中")
	for iter := 0; iter < 300; iter++ {
		nWords := 1 + rng.Intn(6)
		words := make([]string, nWords)
		for i := range words {
			wl := 1 + rng.Intn(6)
			w := make([]rune, wl)
			for j := range w {
				w[j] = alphabet[rng.Intn(len(alphabet))]
			}
			words[i] = string(w)
		}
		d, err := NewDictionary(words)
		if err != nil {
			t.Fatalf("NewDictionary: %v", err)
		}
		tl := rng.Intn(200)
		tr := make([]rune, tl)
		for i := range tr {
			tr[i] = alphabet[rng.Intn(len(alphabet))]
		}
		text := []byte(string(tr))
		want := referenceMask(words, text)
		// 随机分段。
		m := d.NewMessage()
		var got []byte
		for i := 0; i < len(text); {
			n := rng.Intn(9)
			if i+n > len(text) {
				n = len(text) - i
			}
			got = append(got, m.Feed(text[i:i+n])...)
			i += n
		}
		got = append(got, m.End()...)
		if !bytes.Equal(got, want) {
			t.Fatalf("iter=%d words=%q text=%q: got %q, want %q",
				iter, words, text, got, want)
		}
	}
}

func TestLoadDictionary(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/words.txt"
	content := "aba\n\n秘密\n🙂🙃\n" // 含空行，应被忽略
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := LoadDictionary(path)
	if err != nil {
		t.Fatal(err)
	}
	got := d.Mask([]byte("xababa秘密🙂🙃y"))
	want := referenceMask([]string{"aba", "秘密", "🙂🙃"}, []byte("xababa秘密🙂🙃y"))
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	// 词典非空校验。
	empty := dir + "/empty.txt"
	os.WriteFile(empty, []byte("\n\n"), 0o644)
	if _, err := LoadDictionary(empty); err == nil {
		t.Fatalf("expected error for empty dictionary")
	}
}

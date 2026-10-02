package masker

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

const perfSize = 64 << 20 // 64 MiB

// xorshift 伪随机填充，避免压缩与重复带来的乐观估计。
func fillRandom(b []byte, seed uint64) {
	s := seed
	for i := range b {
		s ^= s << 13
		s ^= s >> 7
		s ^= s << 17
		b[i] = byte('a' + s%26)
	}
}

func feedAll(s *Session, data []byte, chunk int) {
	for len(data) > 0 {
		n := chunk
		if n > len(data) {
			n = len(data)
		}
		_ = s.Submit(data[:n])
		data = data[n:]
	}
	_ = s.Finish()
}

func runPerf(t *testing.T, name string, d *Dictionary, input []byte) time.Duration {
	t.Helper()
	s := d.NewSession()
	start := time.Now()
	feedAll(s, input, 64*1024)
	el := time.Since(start)
	t.Logf("%s: %d bytes in %v (%.1f MiB/s)", name, len(input), el, float64(len(input))/(1<<20)/el.Seconds())
	if el > 15*time.Second {
		t.Fatalf("%s: took %v, exceeds 15s", name, el)
	}
	return el
}

func TestPerfPlainASCII(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf test in short mode")
	}
	// 50000 个词，合计约 100 万码点。
	words := make([]string, 0, 50000)
	seed := uint64(12345)
	for len(words) < 50000 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		n := 8 + int(seed%17)
		var sb strings.Builder
		for i := 0; i < n; i++ {
			seed ^= seed << 13
			seed ^= seed >> 7
			seed ^= seed << 17
			sb.WriteByte(byte('a' + seed%26))
		}
		words = append(words, sb.String())
	}
	d := newDict(t, words...)
	input := make([]byte, perfSize)
	fillRandom(input, 999)
	runPerf(t, "plain-ascii-50k-words", d, input)
}

func TestPerfChainWorstCase(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf test in short mode")
	}
	// 词典为 a^1 .. a^1024，输入全为 a：每个位置都有命中。
	words := make([]string, 1024)
	for i := range words {
		words[i] = strings.Repeat("a", i+1)
	}
	d := newDict(t, words...)
	input := []byte(strings.Repeat("a", perfSize))
	runPerf(t, "chain-a^1..1024-all-a", d, input)
}

func TestPerfSharedPrefixMiss(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf test in short mode")
	}
	// 大量共享 1023 个 a 前缀的词，输入反复走到深度 1023 后失配。
	words := make([]string, 0, 50000)
	prefix := strings.Repeat("a", 1000)
	seed := uint64(7)
	for len(words) < 50000 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		words = append(words, fmt.Sprintf("%s%x", prefix, seed))
	}
	d := newDict(t, words...)
	block := []byte(strings.Repeat("a", 1023) + "!")
	input := make([]byte, 0, perfSize)
	for len(input) < perfSize {
		input = append(input, block...)
	}
	runPerf(t, "shared-prefix-miss", d, input)
}

func TestSessionMemoryBound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf test in short mode")
	}
	words := make([]string, 1024)
	for i := range words {
		words[i] = strings.Repeat("a", i+1)
	}
	d := newDict(t, words...)

	runtime.GC()
	var m0 runtime.MemStats
	runtime.ReadMemStats(&m0)

	s := d.NewSession()
	input := []byte(strings.Repeat("a", 32<<20))
	feedAll(s, input, 4096)

	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	growth := int64(m1.HeapAlloc) - int64(m0.HeapAlloc)
	t.Logf("session heap growth after 32 MiB: %d bytes (%.1f KiB)", growth, float64(growth)/1024)
	if growth > 1<<20 {
		t.Fatalf("session memory growth %d exceeds 1 MiB", growth)
	}
}

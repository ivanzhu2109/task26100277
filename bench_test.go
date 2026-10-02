package masker

import (
	"math/rand"
	"runtime"
	"strings"
	"testing"
)

const benchSize = 64 << 20 // 64 MiB

// benchDict 生成 50000 个词、合计约 90 万码点的词典（确定性随机）。
func benchDictWords() []string {
	rng := rand.New(rand.NewSource(7))
	words := make([]string, 50000)
	for i := range words {
		n := 4 + rng.Intn(32)
		b := make([]byte, n)
		for j := range b {
			b[j] = byte('a' + rng.Intn(26))
		}
		words[i] = string(b)
	}
	return words
}

// runStream 以 64 KiB 片段流式处理 data，丢弃输出（仅统计长度）。
func runStream(d *Dictionary, data []byte) int {
	m := d.NewMessage()
	total := 0
	for i := 0; i < len(data); i += 64 << 10 {
		end := i + 64<<10
		if end > len(data) {
			end = len(data)
		}
		total += len(m.Feed(data[i:end]))
	}
	total += len(m.End())
	return total
}

func benchASCII(b *testing.B, words []string, makeInput func() []byte) {
	d, err := NewDictionary(words)
	if err != nil {
		b.Fatal(err)
	}
	data := makeInput()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runStream(d, data)
	}
}

// BenchmarkASCII64MiB 5 万随机词 + 64 MiB 纯 ASCII。
func BenchmarkASCII64MiB(b *testing.B) {
	benchASCII(b, benchDictWords(), func() []byte {
		rng := rand.New(rand.NewSource(9))
		data := make([]byte, benchSize)
		for i := range data {
			data[i] = byte('a' + rng.Intn(26))
		}
		return data
	})
}

// BenchmarkAllAs 词典为 a..a^1024 全部词，输入为 64 MiB 连续 a（每位置都命中）。
func BenchmarkAllAs(b *testing.B) {
	words := make([]string, 1024)
	for i := range words {
		words[i] = strings.Repeat("a", i+1)
	}
	benchASCII(b, words, func() []byte {
		return []byte(strings.Repeat("a", benchSize))
	})
}

// BenchmarkSharedPrefixMiss 大量共享前缀但最终不命中的词典与输入。
func BenchmarkSharedPrefixMiss(b *testing.B) {
	rng := rand.New(rand.NewSource(11))
	prefix := strings.Repeat("ab", 512) // 1024 字符公共前缀
	words := make([]string, 50000)
	for i := range words {
		words[i] = prefix + string(rune('a'+rng.Intn(26))) + string(rune('a'+i%26))
	}
	benchASCII(b, words, func() []byte {
		// 反复走上 1024 字符前缀，然后以不命中的字符收尾。
		unit := prefix + "zz"
		var sb strings.Builder
		for sb.Len() < benchSize {
			sb.WriteString(unit)
		}
		return []byte(sb.String()[:benchSize])
	})
}

// TestMemoryPerMessage 实测单条消息处理过程的额外存活内存：
// 处理 256 MiB 数据前后堆存活字节增量应远小于 1 MiB（不随消息长度增长）。
func TestMemoryPerMessage(t *testing.T) {
	d, err := NewDictionary(benchDictWords())
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 256<<20)
	rng := rand.New(rand.NewSource(3))
	for i := range data {
		data[i] = byte('a' + rng.Intn(26))
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	m := d.NewMessage()
	total := 0
	for i := 0; i < len(data); i += 64 << 10 {
		end := i + 64<<10
		if end > len(data) {
			end = len(data)
		}
		total += len(m.Feed(data[i:end]))
	}
	total += len(m.End())
	runtime.GC()
	runtime.ReadMemStats(&after)
	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("processed %d MiB, output %d bytes, HeapAlloc growth = %d bytes (%.1f KiB)",
		len(data)>>20, total, growth, float64(growth)/1024)
	if growth > 1<<20 {
		t.Fatalf("per-message extra live memory %d bytes exceeds 1 MiB", growth)
	}
}

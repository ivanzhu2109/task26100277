# masker — 流式聊天消息敏感词遮罩库

由消息接收层调用：词典存于本地 UTF-8 文件（每行一词），库按片段增量返回
遮罩结果，调用方负责保存；原文不落盘。

## 设计

- 词典构建为 Aho-Corasick 自动机（按 Unicode 码点、区分大小写、不做规范化），
  加载后固定不变，可安全地被任意多个消息处理过程并发共享。
- 每条消息一个 `Message`：增量 UTF-8 解码（片段可切在多字节码点内部），
  每个码点推进自动机；以当前码点结尾的最长命中（失配链上更短的命中必被它
  包含）记录为一个覆盖区间，进入单调队列，均摊 O(1)。
- 发射不变式：位置 q 的码点只有在处理完位置 q+maxLen-1 后才输出
  （任何覆盖 q 的命中必然已经出现过），因此每次 `Feed` 返回时未输出的完整
  码点数 ≤ 最长词码点数 − 1；各次输出拼接与一次性遮罩逐字节一致。
- 单条消息额外存活内存 O(maxLen)：环形缓冲 maxLen 个码点 + maxLen+1 个区间，
  maxLen=1024 时约 24 KiB，远低于 1 MiB，且不随消息累计长度增长。

## 调用方式

```go
dict, err := masker.LoadDictionary("/path/to/words.txt") // 或 masker.NewDictionary(words)
if err != nil { /* ... */ }

// 每条消息创建一个处理过程；不同消息之间不会跨界匹配。
msg := dict.NewMessage()

var saved []byte                      // 调用方保存：结果只追加
saved = append(saved, msg.Feed(chunk1)...) // 片段可为空、可切在码点内部
saved = append(saved, msg.Feed(chunk2)...)
saved = append(saved, msg.End()...)        // 结束：取回全部剩余内容
```

一次性遮罩完整文本可用 `dict.Mask(text)`。`Feed`/`End` 不是并发安全的，
同一 `Message` 应由单个 goroutine 顺序调用；`End` 之后不得再 `Feed`。

## 测试

```sh
go test ./...          # 全部正确性测试 + 单条消息内存实测
go test -run '^$' -bench . -benchtime 1x   # 64 MiB 性能基准
```

正确性测试以独立的朴素一次性匹配实现（`referenceMask`）为基准逐字节对比，
覆盖：重叠词（aba/bab → 5 星）、自重叠、词互相包含、emoji 与多字节码点被
拆分、空片段、大小写敏感、1023 字符长前缀最终失配（不提前遮罩）、同一消息
多种分段方式结果一致、未输出码点数上限、随机词典/文本/分段模糊测试。

## 实测（Apple M1 Max，单核，Go 1.27）

| 场景 | 规模 | 耗时 | 吞吐 |
|---|---|---|---|
| 5 万随机词（约 90 万码点） | 64 MiB 纯 ASCII | ~5.8 s | ~11.5 MB/s |
| 词典为 a¹..a¹⁰²⁴ 全部词 | 64 MiB 连续 a（每位置都命中） | ~0.97 s | ~69 MB/s |
| 5 万词共享 1024 字符前缀、输入最终失配 | 64 MiB | ~0.74 s | ~91 MB/s |

单条消息处理 256 MiB 文本前后，堆存活字节增量为 0（实测为负值，即 GC 后
无增长），峰值额外存活内存约 24 KiB（maxLen=1024），远低于 1 MiB 上限。

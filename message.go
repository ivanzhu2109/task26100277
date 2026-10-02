package masker

import "unicode/utf8"

// pendingRune 是环形缓冲中一个待输出码点的原始字节。
type pendingRune struct {
	b [utf8.UTFMax]byte // 原始 UTF-8 字节
	l uint8             // 字节长度
}

// coverEntry 表示一个命中区间 [start, end]（码点位置，闭区间）。
type coverEntry struct {
	end   int64
	start int64
}

// Message 是单条消息的流式遮罩处理过程。不同 Message 之间互不影响，
// 不发生跨界匹配。额外存活内存为 O(最长词码点数)，与消息累计长度无关。
//
// 覆盖判定：命中区间按 end 递增产生，发射位置也递增。用单调队列维护
// 当前窗口内 start 最小的区间，发射位置 q 被覆盖当且仅当队列头部区间
// 满足 start <= q <= end。均摊 O(1)，避免逐点回写标记。
type Message struct {
	d     *Dictionary
	state int32 // 自动机当前状态

	ring  []pendingRune // 待输出码点环形缓冲，容量 maxLen
	head  int           // 最老待输出码点的下标
	count int           // 待输出码点数量，Feed 返回时恒定 <= maxLen-1

	cover     []coverEntry // 单调队列（环形），容量 maxLen+1
	coverHead int
	coverLen  int

	pos     int64 // 已消费的码点数（当前码点位置 = pos-1）
	emitPos int64 // 下一个待发射码点的位置

	partial    [utf8.UTFMax]byte // 末尾尚未完整的 UTF-8 序列
	partialLen int
	ended      bool
}

// NewMessage 创建一条新消息的处理过程。
func (d *Dictionary) NewMessage() *Message {
	return &Message{
		d:     d,
		ring:  make([]pendingRune, d.MaxWordLen()),
		cover: make([]coverEntry, d.MaxWordLen()+1),
	}
}

// Feed 提交一个内容片段（可以为空，可以切在多字节码点内部），
// 返回本次可以确定输出的遮罩结果。多次返回值按序拼接，
// 与对完整原文一次性遮罩的结果一致。已返回内容只增不改。
func (m *Message) Feed(chunk []byte) []byte {
	if m.ended {
		panic("masker: Feed after End")
	}
	out := make([]byte, 0, len(chunk))
	for _, c := range chunk {
		if m.partialLen == 0 && c < utf8.RuneSelf {
			// ASCII 快路径：单字节即完整码点。
			m.partial[0] = c
			out = m.consumeRune(rune(c), m.partial[:1], out)
			continue
		}
		m.partial[m.partialLen] = c
		m.partialLen++
		if !utf8.FullRune(m.partial[:m.partialLen]) {
			continue
		}
		r, size := utf8.DecodeRune(m.partial[:m.partialLen])
		out = m.consumeRune(r, m.partial[:size], out)
		m.partialLen = 0
	}
	return out
}

// consumeRune 处理一个完整码点：推进自动机、记录命中区间、输出已确定的码点。
func (m *Message) consumeRune(r rune, b []byte, out []byte) []byte {
	m.state = m.d.step(m.state, r)
	p := m.pos
	m.pos++

	// 当前码点的原始字节入环。
	idx := (m.head + m.count) % len(m.ring)
	entry := &m.ring[idx]
	entry.l = uint8(copy(entry.b[:], b))
	m.count++

	// 以 p 结尾的最长命中覆盖 [p-ml+1, p]；更短的命中（失配链上）被它包含。
	if ml := int64(m.d.maxMatch[m.state]); ml > 0 {
		m.pushCover(coverEntry{end: p, start: p - ml + 1})
	}

	// 只保留 maxLen-1 个未输出码点：更早的码点不可能再被任何命中覆盖。
	for m.count > m.d.MaxWordLen()-1 {
		out = m.emit(out)
	}
	return out
}

// pushCover 按 end 递增顺序加入命中区间，维护 start 递减的单调队列。
func (m *Message) pushCover(e coverEntry) {
	for m.coverLen > 0 {
		back := (m.coverHead + m.coverLen - 1) % len(m.cover)
		if m.cover[back].start < e.start {
			break
		}
		m.coverLen--
	}
	m.cover[(m.coverHead+m.coverLen)%len(m.cover)] = e
	m.coverLen++
}

// covered 判断下一个待发射位置是否被某个命中区间覆盖。
func (m *Message) covered() bool {
	for m.coverLen > 0 && m.cover[m.coverHead].end < m.emitPos {
		m.coverHead = (m.coverHead + 1) % len(m.cover)
		m.coverLen--
	}
	return m.coverLen > 0 && m.cover[m.coverHead].start <= m.emitPos
}

// emit 弹出最老的待输出码点。
func (m *Message) emit(out []byte) []byte {
	entry := &m.ring[m.head]
	if m.covered() {
		out = append(out, '*')
	} else {
		out = append(out, entry.b[:entry.l]...)
	}
	m.head = (m.head + 1) % len(m.ring)
	m.count--
	m.emitPos++
	return out
}

// End 结束消息，返回全部剩余遮罩结果。之后不应再调用 Feed。
func (m *Message) End() []byte {
	if m.ended {
		panic("masker: End called twice")
	}
	m.ended = true
	out := make([]byte, 0, m.count)
	for m.count > 0 {
		out = m.emit(out)
	}
	// 完整消息保证是合法 UTF-8，正常不会走到这里；
	// 若有残缺的末尾字节，原样输出以保证不丢数据。
	out = append(out, m.partial[:m.partialLen]...)
	m.partialLen = 0
	return out
}

// Mask 对完整文本一次性遮罩，等价于 NewMessage + Feed + End。
func (d *Dictionary) Mask(text []byte) []byte {
	m := d.NewMessage()
	out := m.Feed(text)
	return append(out, m.End()...)
}

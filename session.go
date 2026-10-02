package masker

import "unicode/utf8"

// Session 是单条消息的流式遮罩处理过程。
// 不同 Session 之间互不影响，不会发生跨界匹配。
//
// 覆盖判定原理：位置 p 被覆盖，当且仅当存在某个命中区间
// [e-m+1, e] 满足 e-m+1 <= p <= e。由于词长不超过 maxWordLen，
// 处理到位置 p+maxWordLen-1 后，p 的覆盖情况即确定。
// 对每个结束位置 e 记 g(e) = e-m+1（m 为该处最长命中长度），
// 则 p 被覆盖当且仅当窗口 [p, p+maxWordLen-1] 内 min(g) <= p。
// 用单调队列维护该滑动最小值，每个码点均摊 O(1)。
type Session struct {
	d     *Dictionary
	state uint32 // 自动机当前状态

	tail    [utf8.UTFMax]byte // 末尾尚不完整的 UTF-8 序列
	tailLen int

	ring []rune // 最近 maxWordLen 个码点，槽位 = 位置 % maxWordLen

	dqG    []int // 单调队列：g 值，递增
	dqE    []int // 单调队列：对应的结束位置，递增
	dqHead int
	dqTail int // 均为逻辑下标，槽位 = 下标 % maxWordLen

	count   int // 已收到的完整码点数
	emitted int // 已输出的码点数
}

func newSession(d *Dictionary) *Session {
	n := d.maxWordLen
	return &Session{
		d:    d,
		ring: make([]rune, n),
		dqG:  make([]int, n),
		dqE:  make([]int, n),
	}
}

// Submit 提交一个内容片段，返回本段可确定的遮罩结果。
// 片段可以为空，也可以切在多字节码点内部。
// 返回内容只能追加，拼接所有返回值即得完整遮罩结果。
func (s *Session) Submit(chunk []byte) []byte {
	out := make([]byte, 0, len(chunk)+8)
	data := chunk
	if s.tailLen > 0 {
		joined := make([]byte, 0, s.tailLen+len(chunk))
		joined = append(joined, s.tail[:s.tailLen]...)
		joined = append(joined, chunk...)
		data = joined
		s.tailLen = 0
	}
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			if !utf8.FullRune(data) {
				s.tailLen = copy(s.tail[:], data)
				break
			}
			// 契约保证输入为合法 UTF-8；非法字节按原样透传。
			s.ring[s.count%len(s.ring)] = r
			s.count++
			s.pushG(s.count-1, s.count) // 永不覆盖
			out = s.drain(out)
			continue
		}
		out = s.feedRune(r, out)
		data = data[size:]
	}
	return out
}

// Finish 结束消息，返回全部剩余内容。
func (s *Session) Finish() []byte {
	out := make([]byte, 0, 16)
	if s.tailLen > 0 {
		// 契约保证完整消息为合法 UTF-8；残留字节按原样透传。
		out = append(out, s.tail[:s.tailLen]...)
		s.tailLen = 0
	}
	for s.emitted < s.count {
		out = s.emit(s.emitted, out)
		s.emitted++
	}
	return out
}

// feedRune 消费一个完整码点，返回追加后的输出。
func (s *Session) feedRune(r rune, out []byte) []byte {
	idx := s.count
	s.ring[idx%len(s.ring)] = r
	s.count++

	next, m := s.d.ac.step(s.state, r)
	s.state = next
	if m > 0 {
		s.pushG(idx, idx-int(m)+1)
	} else {
		s.pushG(idx, idx+1) // 无命中：g = +inf 的等价表示（> 任何 p <= idx）
	}

	return s.drain(out)
}

// drain 输出所有已确定的位置。
func (s *Session) drain(out []byte) []byte {
	idx := s.count - 1
	for s.emitted <= idx-(s.d.maxWordLen-1) {
		out = s.emit(s.emitted, out)
		s.emitted++
	}
	return out
}

// pushG 将位置 e 的 g 值压入单调队列，保持 g 递增。
func (s *Session) pushG(e, g int) {
	n := len(s.dqG)
	for s.dqTail > s.dqHead && s.dqG[(s.dqTail-1)%n] >= g {
		s.dqTail--
	}
	s.dqG[s.dqTail%n] = g
	s.dqE[s.dqTail%n] = e
	s.dqTail++
}

// emit 输出位置 p 的最终结果：覆盖则为 '*'，否则为原始码点。
func (s *Session) emit(p int, out []byte) []byte {
	n := len(s.dqG)
	for s.dqHead < s.dqTail && s.dqE[s.dqHead%n] < p {
		s.dqHead++
	}
	covered := s.dqHead < s.dqTail && s.dqG[s.dqHead%n] <= p
	// p 已输出，e <= p 的表项不再被任何后续位置使用，弹出以限制队列长度。
	for s.dqHead < s.dqTail && s.dqE[s.dqHead%n] <= p {
		s.dqHead++
	}
	if covered {
		return append(out, '*')
	}
	return utf8.AppendRune(out, s.ring[p%len(s.ring)])
}

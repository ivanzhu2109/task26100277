package masker

import "testing"

func TestAutomatonStep(t *testing.T) {
	a := buildAutomaton([]string{"aba", "bab", "中文字", "字正"})
	type want struct {
		stateIsNonZero bool
		matchLen       uint32
	}
	input := []rune("ababa中文字正")
	expected := []uint32{0, 0, 3, 3, 3, 0, 0, 3, 2}
	state := uint32(0)
	for i, r := range input {
		next, m := a.step(state, r)
		state = next
		if m != expected[i] {
			t.Fatalf("pos %d (%q): got matchLen %d, want %d", i, r, m, expected[i])
		}
	}
}

func TestAutomatonSelfOverlap(t *testing.T) {
	a := buildAutomaton([]string{"aa"})
	state := uint32(0)
	for i := 0; i < 5; i++ {
		next, m := a.step(state, 'a')
		state = next
		if i == 0 && m != 0 {
			t.Fatalf("first 'a' should not match, got %d", m)
		}
		if i > 0 && m != 2 {
			t.Fatalf("pos %d: got matchLen %d, want 2", i, m)
		}
	}
}

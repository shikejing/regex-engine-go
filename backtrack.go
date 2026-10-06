package regex

import "unicode/utf8"

// backtrack.go implements a depth-first backtracking VM used only for
// patterns that contain backreferences, which cannot be executed by
// the linear-time Pike VM. The search is bounded by a step budget;
// exhausting the budget aborts the search and is reported to the
// caller as ErrBacktrackLimit rather than as a (possibly wrong)
// match result.
//
// Semantics match the Pike VM: leftmost-first. Start positions are
// tried left to right; within a start position, alternatives are
// explored in priority order (greedy quantifiers try more iterations
// first, lazy ones fewer, earlier alternation branches first), and
// the first complete match found wins.

// btMachine holds per-search mutable state for the backtracking VM.
type btMachine struct {
	p        *prog
	s        string
	budget   int
	exceeded bool
	loopPos  []int // per-pc input position of the active loop entry, -1 = none
	result   []int // capture slots of the successful match
}

// backtrackSearch returns the capture slots of the leftmost,
// priority-ordered match. exceeded is true if the step budget ran
// out before the search completed; in that case caps is nil.
func backtrackSearch(p *prog, s string, anchored bool, budget int) (caps []int, exceeded bool) {
	m := &btMachine{
		p:       p,
		s:       s,
		budget:  budget,
		loopPos: make([]int, len(p.insts)),
	}
	for i := range m.loopPos {
		m.loopPos[i] = -1
	}
	last := 0
	if !anchored {
		last = len(s)
	}
	for start := 0; start <= last; start++ {
		caps := make([]int, p.numSlots())
		for i := range caps {
			caps[i] = -1
		}
		if m.run(0, start, caps) {
			return m.result, false
		}
		if m.exceeded {
			return nil, true
		}
	}
	return nil, false
}

// run explores the program from (pc, pos) in priority order and
// returns true on the first complete match, with caps holding the
// final capture slots. caps is treated as immutable: opSave installs
// a fresh copy.
func (m *btMachine) run(pc int, pos int, caps []int) bool {
	s := m.s
	for {
		if m.budget <= 0 {
			m.exceeded = true
			return false
		}
		m.budget--
		in := &m.p.insts[pc]
		switch in.op {
		case opChar:
			if pos >= len(s) {
				return false
			}
			r, size := utf8.DecodeRuneInString(s[pos:])
			if r != rune(in.arg) {
				return false
			}
			pos += size
			pc = int(in.x)
		case opAny:
			if pos >= len(s) {
				return false
			}
			r, size := utf8.DecodeRuneInString(s[pos:])
			if r == '\n' {
				return false
			}
			pos += size
			pc = int(in.x)
		case opClass:
			if pos >= len(s) {
				return false
			}
			r, size := utf8.DecodeRuneInString(s[pos:])
			if !m.p.classes[in.arg].contains(r) {
				return false
			}
			pos += size
			pc = int(in.x)
		case opJmp:
			pc = int(in.x)
		case opSave:
			nc := make([]int, len(caps))
			copy(nc, caps)
			nc[in.arg] = pos
			caps = nc
			pc = int(in.x)
		case opAssert:
			if !checkAssertAt(s, in.arg, pos) {
				return false
			}
			pc = int(in.x)
		case opBackref:
			g := int(in.arg)
			lo, hi := caps[2*g], caps[2*g+1]
			if lo < 0 || hi < lo {
				// Group did not participate: the backreference
				// fails to match (PCRE semantics).
				return false
			}
			n := hi - lo
			if pos+n > len(s) || s[pos:pos+n] != s[lo:hi] {
				return false
			}
			pos += n
			pc = int(in.x)
		case opSplit:
			if m.p.loopHead[pc] {
				// Zero-progress loop iterations are cut: re-entering
				// the same loop head at the same input position
				// cannot lead to a new match.
				if m.loopPos[pc] == pos {
					return false
				}
				old := m.loopPos[pc]
				m.loopPos[pc] = pos
				if m.run(int(in.x), pos, caps) {
					m.loopPos[pc] = old
					return true
				}
				if m.exceeded {
					m.loopPos[pc] = old
					return false
				}
				ok := m.run(int(in.y), pos, caps)
				m.loopPos[pc] = old
				return ok
			}
			if m.run(int(in.x), pos, caps) {
				return true
			}
			if m.exceeded {
				return false
			}
			pc = int(in.y)
		case opMatch:
			m.result = caps
			return true
		}
	}
}

// checkAssertAt evaluates a zero-width assertion in s at byte offset
// pos. Shared by the backtracking VM.
func checkAssertAt(s string, kind uint32, pos int) bool {
	switch kind {
	case assertBeginText:
		return pos == 0
	case assertEndText:
		return pos == len(s)
	case assertBeginLine:
		return pos == 0 || s[pos-1] == '\n'
	case assertEndLine:
		return pos == len(s) || s[pos] == '\n'
	case assertWordBoundary, assertNotWordBoundary:
		before := pos > 0 && isWordByte(s[pos-1])
		after := pos < len(s) && isWordByte(s[pos])
		if kind == assertWordBoundary {
			return before != after
		}
		return before == after
	}
	return false
}

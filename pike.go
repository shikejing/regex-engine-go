package regex

import "unicode/utf8"

// pike.go implements a Thompson NFA simulation ("Pike VM"). It runs
// in O(len(input) * len(program)) time and O(len(program) * numSlots)
// memory, never backtracking, so execution cost grows linearly with
// the input size for any pattern without backreferences.
//
// Match semantics: threads are kept in priority order. opMatch is an
// ordinary thread; when the highest-priority surviving thread is an
// opMatch, its captures are recorded and all lower-priority threads
// are cut. This yields leftmost-first (Perl-like) semantics: greedy
// quantifiers prefer more iterations, lazy ones fewer, and earlier
// alternation branches are preferred.

// thread is one active NFA state with its capture slots.
type thread struct {
	pc   int
	caps []int
}

// threadList is the set of threads alive at one input position.
type threadList struct {
	dense []thread
	seen  []uint32 // per-pc generation stamp
	gen   uint32
}

func (l *threadList) reset() {
	l.dense = l.dense[:0]
	l.gen++
}

func (l *threadList) mark(pc int) bool {
	if l.seen[pc] == l.gen {
		return false
	}
	l.seen[pc] = l.gen
	return true
}

// pikeMachine holds per-search mutable state. A fresh machine is
// created per search, so a compiled Regexp stays immutable and safe
// for concurrent use.
type pikeMachine struct {
	p *prog
	s string
}

// pikeSearch returns the capture slots of the leftmost,
// priority-ordered match, or nil if there is no match. If anchored
// is true, only matches starting at offset 0 are considered.
func pikeSearch(p *prog, s string, anchored bool) []int {
	m := &pikeMachine{p: p, s: s}
	clist := &threadList{dense: make([]thread, 0, len(p.insts)), seen: make([]uint32, len(p.insts)), gen: 1}
	nlist := &threadList{dense: make([]thread, 0, len(p.insts)), seen: make([]uint32, len(p.insts)), gen: 1}

	matched := false
	var matchCap []int

	pos := 0
	for {
		// Inject a new start thread at this position with the lowest
		// priority, unless a match was already found. Anchored
		// searches inject only at offset 0.
		if !matched && (!anchored || pos == 0) {
			caps := make([]int, p.numSlots())
			for i := range caps {
				caps[i] = -1
			}
			m.addThread(clist, 0, pos, caps)
		}

		if pos == len(s) {
			// End of input: the highest-priority opMatch thread, if
			// any, wins.
			for _, t := range clist.dense {
				if p.insts[t.pc].op == opMatch {
					matched = true
					matchCap = t.caps
					break
				}
			}
			break
		}

		r, size := utf8.DecodeRuneInString(s[pos:])
		next := pos + size
		nlist.reset()
		for _, t := range clist.dense {
			if p.insts[t.pc].op == opMatch {
				// Highest-priority thread matched: record and cut
				// all lower-priority threads.
				matched = true
				matchCap = t.caps
				break
			}
			m.step(t, r, nlist, next)
		}
		clist, nlist = nlist, clist
		pos = next
		if len(clist.dense) == 0 && (matched || anchored) {
			break
		}
	}
	if !matched {
		return nil
	}
	return matchCap
}

// step advances one thread across rune r, appending successor
// threads to nlist whose closure is evaluated at offset next.
func (m *pikeMachine) step(t thread, r rune, nlist *threadList, next int) {
	in := &m.p.insts[t.pc]
	switch in.op {
	case opChar:
		if r == rune(in.arg) {
			m.addThread(nlist, int(in.x), next, t.caps)
		}
	case opAny:
		if r != '\n' {
			m.addThread(nlist, int(in.x), next, t.caps)
		}
	case opClass:
		if m.p.classes[in.arg].contains(r) {
			m.addThread(nlist, int(in.x), next, t.caps)
		}
	}
	// Epsilon instructions are resolved inside addThread; opBackref
	// never appears in programs executed by the Pike VM.
}

// addThread adds pc and its epsilon closure to list, in priority
// order. pos is the input offset where the closure is evaluated.
func (m *pikeMachine) addThread(list *threadList, pc int, pos int, caps []int) {
	if !list.mark(pc) {
		return
	}
	in := &m.p.insts[pc]
	switch in.op {
	case opJmp:
		m.addThread(list, int(in.x), pos, caps)
	case opSplit:
		m.addThread(list, int(in.x), pos, caps)
		m.addThread(list, int(in.y), pos, caps)
	case opSave:
		newCaps := make([]int, len(caps))
		copy(newCaps, caps)
		newCaps[in.arg] = pos
		m.addThread(list, int(in.x), pos, newCaps)
	case opAssert:
		if m.checkAssert(in.arg, pos) {
			m.addThread(list, int(in.x), pos, caps)
		}
	default:
		// Consuming instructions and opMatch become dense threads.
		list.dense = append(list.dense, thread{pc: pc, caps: caps})
	}
}

// checkAssert evaluates a zero-width assertion at byte offset pos.
func (m *pikeMachine) checkAssert(kind uint32, pos int) bool {
	s := m.s
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

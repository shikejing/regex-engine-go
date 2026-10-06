package regex

import (
	"fmt"
	"unicode/utf8"
)

// Error describes a pattern compilation error with the byte offset
// of the offending position in the pattern.
type Error struct {
	Pos int
	Msg string
}

func (e *Error) Error() string {
	return fmt.Sprintf("regex: syntax error at offset %d: %s", e.Pos, e.Msg)
}

// nodeKind enumerates AST node types produced by the parser.
type nodeKind uint8

const (
	kEmpty   nodeKind = iota // matches empty string
	kChar                    // single literal rune
	kClass                   // character class
	kAny                     // . (any rune except '\n')
	kConcat                  // sequence of nodes
	kAlt                     // alternation of nodes
	kRepeat                  // {min,max} repetition, greedy or lazy
	kGroup                   // capturing (cap>=1) or non-capturing (cap==0) group
	kAssert                  // zero-width assertion
	kBackref                 // backreference to a capture group
)

// assertion kinds stored in node.arg / inst.arg for kAssert.
const (
	assertBeginLine       uint32 = iota // ^
	assertEndLine                       // $
	assertBeginText                     // \A
	assertEndText                       // \z
	assertWordBoundary                  // \b
	assertNotWordBoundary               // \B
)

// maxRepeat bounds counted repetition {n,m} so that program size
// (and thus compile time and memory) stays bounded.
const maxRepeat = 1000

type node struct {
	kind     nodeKind
	r        rune       // kChar
	class    *charClass // kClass
	children []*node    // kConcat, kAlt
	sub      *node      // kRepeat, kGroup
	min, max int        // kRepeat; max<0 means unbounded
	greedy   bool       // kRepeat
	cap      int        // kGroup: 0 = non-capturing
	arg      uint32     // kAssert kind, kBackref group number
}

// -------------------------------------------------------------------
// character classes
// -------------------------------------------------------------------

type runeRange struct{ lo, hi rune }

// charClass is a set of runes represented as sorted, disjoint,
// non-adjacent ranges.
type charClass struct {
	ranges []runeRange
}

func (c *charClass) add(lo, hi rune) {
	c.ranges = append(c.ranges, runeRange{lo, hi})
}

// normalize sorts ranges and merges overlapping or adjacent ones.
func (c *charClass) normalize() {
	rs := c.ranges
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j-1].lo > rs[j].lo; j-- {
			rs[j-1], rs[j] = rs[j], rs[j-1]
		}
	}
	out := rs[:0]
	for _, r := range rs {
		if n := len(out); n > 0 && r.lo <= out[n-1].hi+1 {
			if r.hi > out[n-1].hi {
				out[n-1].hi = r.hi
			}
			continue
		}
		out = append(out, r)
	}
	c.ranges = out
}

// negate complements the class over [0, utf8.MaxRune].
// The class must be normalized first.
func (c *charClass) negate() {
	var out []runeRange
	next := rune(0)
	for _, r := range c.ranges {
		if r.lo > next {
			out = append(out, runeRange{next, r.lo - 1})
		}
		if r.hi+1 > next {
			next = r.hi + 1
		}
	}
	if next <= utf8.MaxRune {
		out = append(out, runeRange{next, utf8.MaxRune})
	}
	c.ranges = out
}

func (c *charClass) contains(r rune) bool {
	lo, hi := 0, len(c.ranges)
	for lo < hi {
		mid := (lo + hi) / 2
		rg := c.ranges[mid]
		if r < rg.lo {
			hi = mid
		} else if r > rg.hi {
			lo = mid + 1
		} else {
			return true
		}
	}
	return false
}

func classDigit() *charClass {
	c := &charClass{}
	c.add('0', '9')
	return c
}

func classWord() *charClass {
	c := &charClass{}
	c.add('0', '9')
	c.add('A', 'Z')
	c.add('_', '_')
	c.add('a', 'z')
	return c
}

func classSpace() *charClass {
	c := &charClass{}
	for _, r := range "\t\n\v\f\r " {
		c.add(r, r)
	}
	return c
}

func negated(c *charClass) *charClass {
	c.normalize()
	c.negate()
	return c
}

var wordClass = classWord()

func isWordByte(b byte) bool {
	return b == '_' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

// -------------------------------------------------------------------
// parser
// -------------------------------------------------------------------

type parser struct {
	s       string
	pos     int // byte offset of next unparsed byte
	numCap  int // number of capturing groups seen so far
	backref []int
}

// parse parses pattern into an AST and returns the root and the
// number of capturing groups (excluding group 0).
func parse(pattern string) (*node, int, error) {
	p := &parser{s: pattern}
	root, err := p.parseAlt()
	if err != nil {
		return nil, 0, err
	}
	if p.pos < len(p.s) {
		// Only possible leftover is an unmatched ')'.
		return nil, 0, &Error{Pos: p.pos, Msg: "unexpected ')'"}
	}
	// Validate backreferences now that the group count is final.
	for _, g := range p.backref {
		if g > p.numCap {
			return nil, 0, &Error{Pos: 0, Msg: fmt.Sprintf("backreference to group %d, but pattern has only %d group(s)", g, p.numCap)}
		}
	}
	return root, p.numCap, nil
}

func (p *parser) errorf(pos int, format string, args ...interface{}) error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

// peek returns the next rune without consuming it.
func (p *parser) peek() (rune, bool) {
	if p.pos >= len(p.s) {
		return 0, false
	}
	r, _ := utf8.DecodeRuneInString(p.s[p.pos:])
	return r, true
}

func (p *parser) next() rune {
	r, size := utf8.DecodeRuneInString(p.s[p.pos:])
	p.pos += size
	return r
}

// parseAlt parses alternation := concat ('|' concat)*.
// It stops at ')' or end of input without consuming the ')'.
func (p *parser) parseAlt() (*node, error) {
	var branches []*node
	for {
		branch, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		branches = append(branches, branch)
		if r, ok := p.peek(); ok && r == '|' {
			p.pos++
			continue
		}
		break
	}
	if len(branches) == 1 {
		return branches[0], nil
	}
	return &node{kind: kAlt, children: branches}, nil
}

// parseConcat parses a sequence of repeated atoms until '|', ')' or
// end of input.
func (p *parser) parseConcat() (*node, error) {
	var items []*node
	for {
		r, ok := p.peek()
		if !ok || r == '|' || r == ')' {
			break
		}
		n, err := p.parseRepeat()
		if err != nil {
			return nil, err
		}
		items = append(items, n)
	}
	switch len(items) {
	case 0:
		return &node{kind: kEmpty}, nil
	case 1:
		return items[0], nil
	default:
		return &node{kind: kConcat, children: items}, nil
	}
}

// parseRepeat parses an atom followed by an optional quantifier.
func (p *parser) parseRepeat() (*node, error) {
	start := p.pos
	atom, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	r, ok := p.peek()
	if !ok {
		return atom, nil
	}
	var min, max int
	switch r {
	case '*':
		min, max = 0, -1
		p.pos++
	case '+':
		min, max = 1, -1
		p.pos++
	case '?':
		min, max = 0, 1
		p.pos++
	case '{':
		mn, mx, ok := p.tryParseCounted()
		if !ok {
			return nil, p.errorf(p.pos, "malformed counted repetition")
		}
		min, max = mn, mx
	default:
		return atom, nil
	}
	if max >= 0 && max < min {
		return nil, p.errorf(start, "invalid repetition range {%d,%d}", min, max)
	}
	if max > maxRepeat || min > maxRepeat {
		return nil, p.errorf(start, "repetition count exceeds limit of %d", maxRepeat)
	}
	greedy := true
	if r2, ok := p.peek(); ok && r2 == '?' {
		greedy = false
		p.pos++
	}
	// A quantifier directly following another quantifier is an error.
	if r3, ok := p.peek(); ok {
		nested := r3 == '*' || r3 == '+' || r3 == '?'
		if r3 == '{' {
			_, _, nested = p.tryParseCountedAt(p.pos)
		}
		if nested {
			return nil, p.errorf(p.pos, "nested repetition operator")
		}
	}
	return &node{kind: kRepeat, sub: atom, min: min, max: max, greedy: greedy}, nil
}

// tryParseCounted attempts to parse "{n}", "{n,}" or "{n,m}" at the
// current position. On success it consumes through '}' and returns
// ok=true. On failure it leaves the position at the '{'.
func (p *parser) tryParseCounted() (min, max int, ok bool) {
	save := p.pos
	p.pos++ // consume '{'
	readNum := func() (int, bool) {
		n := 0
		digits := 0
		for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
			n = n*10 + int(p.s[p.pos]-'0')
			if n > 1_000_000 {
				return 0, false
			}
			p.pos++
			digits++
		}
		return n, digits > 0
	}
	lo, hasLo := readNum()
	if !hasLo {
		p.pos = save
		return 0, 0, false
	}
	if p.pos < len(p.s) && p.s[p.pos] == '}' {
		p.pos++
		return lo, lo, true
	}
	if p.pos >= len(p.s) || p.s[p.pos] != ',' {
		p.pos = save
		return 0, 0, false
	}
	p.pos++ // consume ','
	if p.pos < len(p.s) && p.s[p.pos] == '}' {
		p.pos++
		return lo, -1, true
	}
	hi, hasHi := readNum()
	if !hasHi || p.pos >= len(p.s) || p.s[p.pos] != '}' {
		p.pos = save
		return 0, 0, false
	}
	p.pos++
	return lo, hi, true
}

// parseAtom parses a single atom.
func (p *parser) parseAtom() (*node, error) {
	start := p.pos
	r := p.next()
	switch r {
	case '(':
		return p.parseGroup(start)
	case '[':
		return p.parseClass(start)
	case '.':
		return &node{kind: kAny}, nil
	case '^':
		return &node{kind: kAssert, arg: assertBeginLine}, nil
	case '$':
		return &node{kind: kAssert, arg: assertEndLine}, nil
	case '*', '+', '?':
		return nil, p.errorf(start, "missing argument to repetition operator %q", string(r))
	case '{':
		if _, _, ok := p.tryParseCountedAt(start); ok {
			return nil, p.errorf(start, "missing argument to repetition operator")
		}
		return &node{kind: kChar, r: '{'}, nil
	case '\\':
		return p.parseEscape(start, false)
	case ')':
		// Handled by callers; should not reach here.
		return nil, p.errorf(start, "unexpected ')'")
	default:
		return &node{kind: kChar, r: r}, nil
	}
}

// tryParseCountedAt is a read-only probe used for the error message
// when '{' begins an atom.
func (p *parser) tryParseCountedAt(pos int) (int, int, bool) {
	save := p.pos
	p.pos = pos
	min, max, ok := p.tryParseCounted()
	p.pos = save
	return min, max, ok
}

// parseGroup parses the body of a group after '(' has been consumed.
func (p *parser) parseGroup(start int) (*node, error) {
	cap := 0
	if p.pos+1 < len(p.s) && p.s[p.pos] == '?' {
		if p.s[p.pos+1] == ':' {
			p.pos += 2
		} else {
			return nil, p.errorf(start, "unsupported group syntax '(?%c' (only (?:...) is supported)", p.s[p.pos+1])
		}
	} else {
		p.numCap++
		cap = p.numCap
	}
	body, err := p.parseAlt()
	if err != nil {
		return nil, err
	}
	if p.pos >= len(p.s) {
		return nil, p.errorf(start, "unclosed group")
	}
	p.pos++ // consume ')'
	return &node{kind: kGroup, sub: body, cap: cap}, nil
}

// parseEscape parses an escape sequence after '\' has been consumed.
// inClass selects character-class escape semantics.
func (p *parser) parseEscape(start int, inClass bool) (*node, error) {
	if p.pos >= len(p.s) {
		return nil, p.errorf(start, "trailing backslash")
	}
	r := p.next()
	switch r {
	case 'd':
		return &node{kind: kClass, class: classDigit()}, nil
	case 'D':
		return &node{kind: kClass, class: negated(classDigit())}, nil
	case 'w':
		return &node{kind: kClass, class: classWord()}, nil
	case 'W':
		return &node{kind: kClass, class: negated(classWord())}, nil
	case 's':
		return &node{kind: kClass, class: classSpace()}, nil
	case 'S':
		return &node{kind: kClass, class: negated(classSpace())}, nil
	case 'n':
		return &node{kind: kChar, r: '\n'}, nil
	case 'r':
		return &node{kind: kChar, r: '\r'}, nil
	case 't':
		return &node{kind: kChar, r: '\t'}, nil
	case 'f':
		return &node{kind: kChar, r: '\f'}, nil
	case 'v':
		return &node{kind: kChar, r: '\v'}, nil
	case 'a':
		return &node{kind: kChar, r: '\a'}, nil
	case 'x':
		v, err := p.parseHex(2, start)
		if err != nil {
			return nil, err
		}
		return &node{kind: kChar, r: rune(v)}, nil
	case 'u':
		v, err := p.parseHex(4, start)
		if err != nil {
			return nil, err
		}
		return &node{kind: kChar, r: rune(v)}, nil
	}
	if inClass && r == 'b' {
		return &node{kind: kChar, r: '\b'}, nil
	}
	if !inClass {
		switch r {
		case 'b':
			return &node{kind: kAssert, arg: assertWordBoundary}, nil
		case 'B':
			return &node{kind: kAssert, arg: assertNotWordBoundary}, nil
		case 'A':
			return &node{kind: kAssert, arg: assertBeginText}, nil
		case 'z':
			return &node{kind: kAssert, arg: assertEndText}, nil
		}
		if r >= '1' && r <= '9' {
			g := int(r - '0')
			p.backref = append(p.backref, g)
			return &node{kind: kBackref, arg: uint32(g)}, nil
		}
	}
	if isAlphaNum(r) {
		return nil, p.errorf(start, "unsupported escape sequence '\\%c'", r)
	}
	// Any non-alphanumeric character escapes to itself.
	return &node{kind: kChar, r: r}, nil
}

func isAlphaNum(r rune) bool {
	return '0' <= r && r <= '9' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z'
}

// parseHex parses exactly n hex digits.
func (p *parser) parseHex(n int, start int) (int, error) {
	v := 0
	for i := 0; i < n; i++ {
		if p.pos >= len(p.s) {
			return 0, p.errorf(start, "truncated hex escape")
		}
		c := p.s[p.pos]
		var d int
		switch {
		case '0' <= c && c <= '9':
			d = int(c - '0')
		case 'a' <= c && c <= 'f':
			d = int(c-'a') + 10
		case 'A' <= c && c <= 'F':
			d = int(c-'A') + 10
		default:
			return 0, p.errorf(start, "invalid hex escape")
		}
		v = v*16 + d
		p.pos++
	}
	return v, nil
}

// parseClass parses a character class after '[' has been consumed.
func (p *parser) parseClass(start int) (*node, error) {
	cls := &charClass{}
	negate := false
	if p.pos < len(p.s) && p.s[p.pos] == '^' {
		negate = true
		p.pos++
	}
	first := true
	for {
		if p.pos >= len(p.s) {
			return nil, p.errorf(start, "unclosed character class")
		}
		if p.s[p.pos] == ']' && !first {
			p.pos++
			break
		}
		first = false
		lo, err := p.parseClassItem(start)
		if err != nil {
			return nil, err
		}
		if lo.class != nil {
			// A class escape (\d etc.) cannot be a range endpoint.
			cls.ranges = append(cls.ranges, lo.class.ranges...)
			continue
		}
		// Possible range: 'a-z'. A '-' is literal if it is the last
		// character before ']'.
		if p.pos < len(p.s) && p.s[p.pos] == '-' &&
			p.pos+1 < len(p.s) && p.s[p.pos+1] != ']' {
			p.pos++ // consume '-'
			hi, err := p.parseClassItem(start)
			if err != nil {
				return nil, err
			}
			if hi.class != nil {
				return nil, p.errorf(p.pos, "character class escape cannot end a range")
			}
			if hi.r < lo.r {
				return nil, p.errorf(p.pos, "invalid character class range")
			}
			cls.add(lo.r, hi.r)
			continue
		}
		cls.add(lo.r, lo.r)
	}
	cls.normalize()
	if negate {
		cls.negate()
	}
	return &node{kind: kClass, class: cls}, nil
}

// parseClassItem parses one item inside a character class: either a
// single rune or a class escape like \d.
func (p *parser) parseClassItem(start int) (*node, error) {
	if p.s[p.pos] == '\\' {
		escStart := p.pos
		p.pos++
		n, err := p.parseEscape(escStart, true)
		if err != nil {
			return nil, err
		}
		if n.kind == kClass {
			return n, nil
		}
		return &node{kind: kChar, r: n.r}, nil
	}
	r := p.next()
	return &node{kind: kChar, r: r}, nil
}

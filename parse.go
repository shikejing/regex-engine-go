package regex

import (
	"fmt"
	"unicode/utf8"
)

// SyntaxError 表示模式串的编译期错误，携带出错位置（字节偏移）。
type SyntaxError struct {
	Pos int
	Msg string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("regex syntax error at offset %d: %s", e.Pos, e.Msg)
}

// maxRepeat 是计数量词允许的最大重复次数，
// 防止编译产物因 {n,m} 展开而失控膨胀。
const maxRepeat = 1000

// parser 把模式串解析为 AST。
type parser struct {
	src     string
	pos     int
	numCaps int
	hasRef  bool
}

// parse 解析整个模式串，返回 AST 根节点、捕获组数量以及是否含反向引用。
func parse(src string) (*node, int, bool, error) {
	p := &parser{src: src}
	root, err := p.parseAlt()
	if err != nil {
		return nil, 0, false, err
	}
	if p.pos != len(p.src) {
		return nil, 0, false, &SyntaxError{Pos: p.pos, Msg: "unexpected ')'"}
	}
	return root, p.numCaps, p.hasRef, nil
}

func (p *parser) errf(pos int, format string, args ...any) error {
	return &SyntaxError{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

// parseAlt 解析选择分支：concat ('|' concat)*
func (p *parser) parseAlt() (*node, error) {
	left, err := p.parseConcat()
	if err != nil {
		return nil, err
	}
	branches := []*node{left}
	for p.pos < len(p.src) && p.src[p.pos] == '|' {
		p.pos++
		right, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		branches = append(branches, right)
	}
	if len(branches) == 1 {
		return left, nil
	}
	return &node{kind: kAlternate, kids: branches}, nil
}

// parseConcat 解析顺序连接，直到遇到 '|'、')' 或结尾。
func (p *parser) parseConcat() (*node, error) {
	var items []*node
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '|' || c == ')' {
			break
		}
		atom, err := p.parseRepeat()
		if err != nil {
			return nil, err
		}
		items = append(items, atom)
	}
	switch len(items) {
	case 0:
		return &node{kind: kEmpty}, nil
	case 1:
		return items[0], nil
	default:
		return &node{kind: kConcat, kids: items}, nil
	}
}

// parseRepeat 解析一个原子后跟零个或多个量词。
func (p *parser) parseRepeat() (*node, error) {
	atom, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	for p.pos < len(p.src) {
		var lo, hi int
		start := p.pos
		switch p.src[p.pos] {
		case '*':
			p.pos++
			lo, hi = 0, -1
		case '+':
			p.pos++
			lo, hi = 1, -1
		case '?':
			p.pos++
			lo, hi = 0, 1
		case '{':
			var ok bool
			lo, hi, ok, err = p.parseCounted()
			if err != nil {
				return nil, err
			}
			if !ok {
				// '{' 后面不是合法计数形式，按字面量处理：
				// 把原子改为 原子+字面量'{' 序列过于复杂，
				// 这里直接报错，语义更明确。
				return nil, p.errf(start, "invalid counted repetition")
			}
		default:
			return atom, nil
		}
		greedy := true
		if p.pos < len(p.src) && p.src[p.pos] == '?' {
			greedy = false
			p.pos++
		}
		atom = &node{kind: kRepeat, lo: lo, hi: hi, greedy: greedy, sub: atom}
	}
	return atom, nil
}

// parseCounted 解析 {n} {n,} {n,m}。ok=false 表示 '{' 后不是计数形式。
func (p *parser) parseCounted() (lo, hi int, ok bool, err error) {
	start := p.pos
	i := start + 1
	readNum := func() (int, bool) {
		j := i
		for j < len(p.src) && p.src[j] >= '0' && p.src[j] <= '9' {
			j++
		}
		if j == i {
			return 0, false
		}
		n := 0
		for ; i < j; i++ {
			n = n*10 + int(p.src[i]-'0')
			if n > maxRepeat {
				return -1, true // 标记超限
			}
		}
		return n, true
	}
	lo, hasLo := readNum()
	if lo < 0 {
		return 0, 0, false, p.errf(start, "repeat count exceeds limit %d", maxRepeat)
	}
	if !hasLo {
		return 0, 0, false, nil
	}
	if i < len(p.src) && p.src[i] == '}' {
		p.pos = i + 1
		return lo, lo, true, nil
	}
	if i >= len(p.src) || p.src[i] != ',' {
		return 0, 0, false, nil
	}
	i++
	hi, hasHi := readNum()
	if hi < 0 {
		return 0, 0, false, p.errf(start, "repeat count exceeds limit %d", maxRepeat)
	}
	if i >= len(p.src) || p.src[i] != '}' {
		return 0, 0, false, nil
	}
	p.pos = i + 1
	if !hasHi {
		return lo, -1, true, nil
	}
	if hi < lo {
		return 0, 0, false, p.errf(start, "repeat range {%d,%d} is inverted", lo, hi)
	}
	return lo, hi, true, nil
}

// parseAtom 解析单个原子。
func (p *parser) parseAtom() (*node, error) {
	start := p.pos
	c := p.src[p.pos]
	switch c {
	case '(':
		p.pos++
		capturing := true
		if p.pos+1 < len(p.src) && p.src[p.pos] == '?' && p.src[p.pos+1] == ':' {
			capturing = false
			p.pos += 2
		}
		// 组编号必须在解析 body 之前分配，
		// 保证与左括号出现顺序一致。
		capIndex := 0
		if capturing {
			p.numCaps++
			capIndex = p.numCaps
		}
		body, err := p.parseAlt()
		if err != nil {
			return nil, err
		}
		if p.pos >= len(p.src) || p.src[p.pos] != ')' {
			return nil, p.errf(start, "missing closing ')'")
		}
		p.pos++
		if !capturing {
			return body, nil
		}
		return &node{kind: kCapture, capIndex: capIndex, body: body}, nil
	case ')':
		return nil, p.errf(start, "unexpected ')'")
	case '[':
		return p.parseClass()
	case '.':
		p.pos++
		return &node{kind: kDot}, nil
	case '^':
		p.pos++
		return &node{kind: kAnchor, anchor: anchorStart}, nil
	case '$':
		p.pos++
		return &node{kind: kAnchor, anchor: anchorEnd}, nil
	case '*', '+', '?':
		return nil, p.errf(start, "quantifier without preceding atom")
	case '{':
		// 单独出现的 '{' 若不是计数形式则按字面量。
		if _, _, ok, err := p.parseCounted(); err != nil {
			return nil, err
		} else if ok {
			return nil, p.errf(start, "quantifier without preceding atom")
		}
		p.pos++
		return &node{kind: kLiteral, r: '{'}, nil
	case '\\':
		return p.parseEscape()
	default:
		r, size := utf8.DecodeRuneInString(p.src[p.pos:])
		p.pos += size
		return &node{kind: kLiteral, r: r}, nil
	}
}

// parseEscape 解析反斜杠转义（可能出现在原子位置或字符类内）。
// 返回节点；字符类转义（\d 等）以 kClass 节点表示。
func (p *parser) parseEscape() (*node, error) {
	start := p.pos
	p.pos++ // 跳过 '\'
	if p.pos >= len(p.src) {
		return nil, p.errf(start, "trailing backslash")
	}
	c := p.src[p.pos]
	p.pos++
	switch c {
	case 'n':
		return &node{kind: kLiteral, r: '\n'}, nil
	case 't':
		return &node{kind: kLiteral, r: '\t'}, nil
	case 'r':
		return &node{kind: kLiteral, r: '\r'}, nil
	case 'f':
		return &node{kind: kLiteral, r: '\f'}, nil
	case 'v':
		return &node{kind: kLiteral, r: '\v'}, nil
	case 'd':
		return &node{kind: kClass, class: classDigits()}, nil
	case 'D':
		return &node{kind: kClass, class: classDigits().negate()}, nil
	case 'w':
		return &node{kind: kClass, class: classWord()}, nil
	case 'W':
		return &node{kind: kClass, class: classWord().negate()}, nil
	case 's':
		return &node{kind: kClass, class: classSpace()}, nil
	case 'S':
		return &node{kind: kClass, class: classSpace().negate()}, nil
	case 'x':
		r, err := p.parseHex(2, start)
		if err != nil {
			return nil, err
		}
		return &node{kind: kLiteral, r: r}, nil
	case 'u':
		r, err := p.parseHex(4, start)
		if err != nil {
			return nil, err
		}
		return &node{kind: kLiteral, r: r}, nil
	default:
		if c >= '1' && c <= '9' {
			p.hasRef = true
			return &node{kind: kBackref, refIndex: int(c - '0')}, nil
		}
		if isEscapablePunct(c) {
			return &node{kind: kLiteral, r: rune(c)}, nil
		}
		return nil, p.errf(start, "unsupported escape \\%c", c)
	}
}

// parseHex 读取 n 位十六进制数。
func (p *parser) parseHex(n int, start int) (rune, error) {
	if p.pos+n > len(p.src) {
		return 0, p.errf(start, "incomplete hex escape")
	}
	v := 0
	for i := 0; i < n; i++ {
		c := p.src[p.pos+i]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return 0, p.errf(start, "invalid hex escape")
		}
		v = v*16 + d
	}
	p.pos += n
	return rune(v), nil
}

func isEscapablePunct(c byte) bool {
	switch c {
	case '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '|', '^', '$', '\\', '/', '-':
		return true
	}
	return false
}

// parseClass 解析字符类 [...]。
func (p *parser) parseClass() (*node, error) {
	start := p.pos
	p.pos++ // 跳过 '['
	negated := false
	if p.pos < len(p.src) && p.src[p.pos] == '^' {
		negated = true
		p.pos++
	}
	var ranges []runeRange
	first := true
	for {
		if p.pos >= len(p.src) {
			return nil, p.errf(start, "missing closing ']'")
		}
		if p.src[p.pos] == ']' && !first {
			p.pos++
			break
		}
		first = false
		lo, inner, err := p.parseClassItem(start)
		if err != nil {
			return nil, err
		}
		if inner != nil {
			// \d \w \s 等内嵌类：并入区间集合
			ranges = append(ranges, inner.ranges...)
			continue
		}
		// 检查区间形式 a-z
		if p.pos < len(p.src) && p.src[p.pos] == '-' &&
			p.pos+1 < len(p.src) && p.src[p.pos+1] != ']' {
			p.pos++ // 跳过 '-'
			hi, inner2, err := p.parseClassItem(start)
			if err != nil {
				return nil, err
			}
			if inner2 != nil {
				return nil, p.errf(start, "character class escape cannot be a range endpoint")
			}
			if hi < lo {
				return nil, p.errf(start, "invalid character range %q-%q", lo, hi)
			}
			ranges = append(ranges, runeRange{lo, hi})
			continue
		}
		ranges = append(ranges, runeRange{lo, lo})
	}
	cls := &charClass{ranges: normalizeRanges(ranges)}
	if negated {
		cls = cls.negate()
	}
	return &node{kind: kClass, class: cls}, nil
}

// parseClassItem 解析字符类中的单个元素。
// 若是 \d \w \s 之类的类转义，返回 inner 非 nil。
func (p *parser) parseClassItem(classStart int) (rune, *charClass, error) {
	c := p.src[p.pos]
	if c != '\\' {
		r, size := utf8.DecodeRuneInString(p.src[p.pos:])
		p.pos += size
		return r, nil, nil
	}
	start := p.pos
	nd, err := p.parseEscape()
	if err != nil {
		return 0, nil, err
	}
	switch nd.kind {
	case kLiteral:
		return nd.r, nil, nil
	case kClass:
		return 0, nd.class, nil
	default:
		return 0, nil, p.errf(start, "escape not allowed in character class")
	}
}

// 预定义字符类。
func classDigits() *charClass {
	return &charClass{ranges: []runeRange{{'0', '9'}}}
}

func classWord() *charClass {
	return &charClass{ranges: []runeRange{{'0', '9'}, {'A', 'Z'}, {'_', '_'}, {'a', 'z'}}}
}

func classSpace() *charClass {
	return &charClass{ranges: []runeRange{
		{'\t', '\r'}, {' ', ' '},
	}}
}

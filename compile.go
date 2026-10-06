package regex

import "fmt"

// instOp enumerates NFA instruction opcodes.
type instOp uint8

const (
	opChar    instOp = iota // match rune arg, go to x
	opClass                 // match classes[arg], go to x
	opAny                   // match any rune except '\n', go to x
	opSplit                 // try x first, then y (priority order)
	opJmp                   // jump to x
	opSave                  // record current pos into slot arg, go to x
	opAssert                // require assertion arg, go to x
	opBackref               // match text captured by group arg, go to x
	opMatch                 // accept
)

type inst struct {
	op  instOp
	arg uint32
	x   uint32
	y   uint32
}

// maxProgram bounds the number of compiled instructions so that
// pathological counted repetitions cannot exhaust memory at compile
// time.
const maxProgram = 100_000

// prog is a compiled, immutable NFA program.
type prog struct {
	insts    []inst
	classes  []charClass
	loopHead []bool // loopHead[i]: inst i is a split that closes a cycle
	numCap   int    // number of capture groups including group 0
	backrefs bool   // program contains backreferences
}

// numSlots returns the number of capture slots (2 per group).
func (p *prog) numSlots() int { return 2 * p.numCap }

// patchRef identifies one dangling out-edge of an instruction.
type patchRef struct {
	idx   int
	field int // 0 => x, 1 => y
}

// frag is a partially compiled fragment with a start instruction and
// a list of dangling out-edges.
type frag struct {
	start int
	out   []patchRef
}

type compiler struct {
	insts    []inst
	classes  []charClass
	backrefs bool
}

// compileProgram compiles an AST into an NFA program. The program
// matches the AST anchored at its start; unanchored (leftmost)
// search is handled by the VMs.
func compileProgram(root *node, numCap int) (*prog, error) {
	c := &compiler{}
	save0, err := c.emit(opSave, 0)
	if err != nil {
		return nil, err
	}
	f, err := c.compileNode(root)
	if err != nil {
		return nil, err
	}
	c.insts[save0].x = uint32(f.start)
	save1, err := c.emit(opSave, 1)
	if err != nil {
		return nil, err
	}
	c.patchAll(f.out, save1)
	match, err := c.emit(opMatch, 0)
	if err != nil {
		return nil, err
	}
	c.insts[save1].x = uint32(match)
	// Mark loop heads: splits with a backward edge close a cycle
	// (only unbounded * and + compile to cycles). The backtracking
	// VM uses this to cut zero-progress iterations.
	loopHead := make([]bool, len(c.insts))
	for i, in := range c.insts {
		if in.op != opSplit {
			continue
		}
		if int(in.x) <= i || int(in.y) <= i {
			loopHead[i] = true
		}
	}
	return &prog{
		insts:    c.insts,
		classes:  c.classes,
		loopHead: loopHead,
		numCap:   numCap + 1,
		backrefs: c.backrefs,
	}, nil
}

func (c *compiler) emit(op instOp, arg uint32) (int, error) {
	if len(c.insts) >= maxProgram {
		return 0, &Error{Pos: 0, Msg: fmt.Sprintf("pattern compiles to more than %d instructions", maxProgram)}
	}
	c.insts = append(c.insts, inst{op: op, arg: arg, x: ^uint32(0), y: ^uint32(0)})
	return len(c.insts) - 1, nil
}

func (c *compiler) setOut(ref patchRef, target int) {
	if ref.field == 0 {
		c.insts[ref.idx].x = uint32(target)
	} else {
		c.insts[ref.idx].y = uint32(target)
	}
}

func (c *compiler) patchAll(refs []patchRef, target int) {
	for _, r := range refs {
		c.setOut(r, target)
	}
}

func (c *compiler) compileNode(n *node) (frag, error) {
	switch n.kind {
	case kEmpty:
		i, err := c.emit(opJmp, 0)
		return frag{start: i, out: []patchRef{{i, 0}}}, err
	case kChar:
		i, err := c.emit(opChar, uint32(n.r))
		return frag{start: i, out: []patchRef{{i, 0}}}, err
	case kAny:
		i, err := c.emit(opAny, 0)
		return frag{start: i, out: []patchRef{{i, 0}}}, err
	case kClass:
		c.classes = append(c.classes, *n.class)
		i, err := c.emit(opClass, uint32(len(c.classes)-1))
		return frag{start: i, out: []patchRef{{i, 0}}}, err
	case kAssert:
		i, err := c.emit(opAssert, n.arg)
		return frag{start: i, out: []patchRef{{i, 0}}}, err
	case kBackref:
		c.backrefs = true
		i, err := c.emit(opBackref, n.arg)
		return frag{start: i, out: []patchRef{{i, 0}}}, err
	case kConcat:
		return c.compileConcat(n.children)
	case kAlt:
		return c.compileAlt(n.children)
	case kGroup:
		return c.compileGroup(n)
	case kRepeat:
		return c.compileRepeat(n)
	}
	return frag{}, &Error{Pos: 0, Msg: "internal: unknown node kind"}
}

func (c *compiler) compileConcat(children []*node) (frag, error) {
	var f frag
	for i, ch := range children {
		g, err := c.compileNode(ch)
		if err != nil {
			return frag{}, err
		}
		if i == 0 {
			f = g
		} else {
			c.patchAll(f.out, g.start)
			f.out = g.out
		}
	}
	return f, nil
}

func (c *compiler) compileAlt(branches []*node) (frag, error) {
	// Chain splits: split(b1, split(b2, ... bn)). Left branches have
	// higher priority, which yields leftmost-first alternation.
	var out []patchRef
	var splits []int
	var starts []int
	for _, b := range branches {
		g, err := c.compileNode(b)
		if err != nil {
			return frag{}, err
		}
		starts = append(starts, g.start)
		out = append(out, g.out...)
	}
	// Emit splits in reverse so earlier branches get priority.
	for i := len(branches) - 2; i >= 0; i-- {
		sp, err := c.emit(opSplit, 0)
		if err != nil {
			return frag{}, err
		}
		c.insts[sp].x = uint32(starts[i])
		if i == len(branches)-2 {
			c.insts[sp].y = uint32(starts[i+1])
		} else {
			c.insts[sp].y = uint32(splits[len(splits)-1])
		}
		splits = append(splits, sp)
	}
	return frag{start: splits[len(splits)-1], out: out}, nil
}

func (c *compiler) compileGroup(n *node) (frag, error) {
	body, err := c.compileNode(n.sub)
	if err != nil {
		return frag{}, err
	}
	if n.cap == 0 {
		return body, nil
	}
	open, err := c.emit(opSave, uint32(2*n.cap))
	if err != nil {
		return frag{}, err
	}
	close, err := c.emit(opSave, uint32(2*n.cap+1))
	if err != nil {
		return frag{}, err
	}
	c.insts[open].x = uint32(body.start)
	c.patchAll(body.out, close)
	return frag{start: open, out: []patchRef{{close, 0}}}, nil
}

// nullable reports whether n can match the empty string. It is used
// to compile unbounded repetition in a way that preserves priority
// order for nullable bodies (see compileStar).
func nullable(n *node) bool {
	switch n.kind {
	case kEmpty, kAssert, kBackref:
		// A backreference may refer to a group that matched empty;
		// treat it as nullable to stay conservative.
		return true
	case kChar, kClass, kAny:
		return false
	case kConcat:
		for _, ch := range n.children {
			if !nullable(ch) {
				return false
			}
		}
		return true
	case kAlt:
		for _, ch := range n.children {
			if nullable(ch) {
				return true
			}
		}
		return false
	case kGroup:
		return nullable(n.sub)
	case kRepeat:
		if n.min == 0 {
			return true
		}
		return nullable(n.sub)
	}
	return false
}

// compileRepeat expands counted repetition:
//
//	x{n,}   => x^(n-1) followed by x+ (a plain loop)
//	x{0,}   => x* (see compileStar)
//	x{n,m}  => x^n followed by nested optionals (x(x(x)?)?)?
//
// The nested-optional form keeps capture and priority semantics
// aligned with the established leftmost-first behavior.
func (c *compiler) compileRepeat(n *node) (frag, error) {
	min, max := n.min, n.max
	var parts []frag
	emit := func() error {
		g, err := c.compileNode(n.sub)
		if err != nil {
			return err
		}
		parts = append(parts, g)
		return nil
	}
	if max < 0 {
		if min == 0 {
			return c.compileStar(n.sub, n.greedy)
		}
		for i := 0; i < min-1; i++ {
			if err := emit(); err != nil {
				return frag{}, err
			}
		}
		plus, err := c.compilePlus(n.sub, n.greedy)
		if err != nil {
			return frag{}, err
		}
		parts = append(parts, plus)
	} else {
		for i := 0; i < min; i++ {
			if err := emit(); err != nil {
				return frag{}, err
			}
		}
		// Nested optional tail: (x(x(x)?)?)? built inside out.
		var tail *frag
		for i := max - 1; i >= min; i-- {
			g, err := c.compileNode(n.sub)
			if err != nil {
				return frag{}, err
			}
			if tail != nil {
				c.patchAll(g.out, tail.start)
				g.out = tail.out
			}
			q, err := c.compileQuest(g, n.greedy)
			if err != nil {
				return frag{}, err
			}
			tail = &q
		}
		if tail != nil {
			parts = append(parts, *tail)
		}
	}
	if len(parts) == 0 {
		// {0,0}: matches empty.
		i, err := c.emit(opJmp, 0)
		return frag{start: i, out: []patchRef{{i, 0}}}, err
	}
	f := parts[0]
	for _, g := range parts[1:] {
		c.patchAll(f.out, g.start)
		f.out = g.out
	}
	return f, nil
}

// compileStar compiles an unbounded repetition of sub. When sub is
// nullable it is compiled as (?:sub+)? so that the empty-match exit
// keeps priority over further iterations of the body; a plain loop
// would order a zero-progress iteration of the body ahead of the
// exit and produce wrong (non-Perl) match boundaries.
func (c *compiler) compileStar(sub *node, greedy bool) (frag, error) {
	if nullable(sub) {
		plus, err := c.compilePlus(sub, greedy)
		if err != nil {
			return frag{}, err
		}
		return c.compileQuest(plus, greedy)
	}
	return c.compileLoop(sub, greedy)
}

// compileLoop compiles sub* for a non-nullable sub: a split whose
// preferred edge enters the body and whose other edge exits, with
// the body looping back to the split.
func (c *compiler) compileLoop(sub *node, greedy bool) (frag, error) {
	g, err := c.compileNode(sub)
	if err != nil {
		return frag{}, err
	}
	sp, err := c.emit(opSplit, 0)
	if err != nil {
		return frag{}, err
	}
	if greedy {
		c.insts[sp].x = uint32(g.start)
		c.patchAll(g.out, sp)
		return frag{start: sp, out: []patchRef{{sp, 1}}}, nil
	}
	c.insts[sp].y = uint32(g.start)
	c.patchAll(g.out, sp)
	return frag{start: sp, out: []patchRef{{sp, 0}}}, nil
}

// compilePlus compiles sub+: one copy of sub followed by a loop
// split that re-enters sub.
func (c *compiler) compilePlus(sub *node, greedy bool) (frag, error) {
	g, err := c.compileNode(sub)
	if err != nil {
		return frag{}, err
	}
	sp, err := c.emit(opSplit, 0)
	if err != nil {
		return frag{}, err
	}
	if greedy {
		c.insts[sp].x = uint32(g.start)
		c.patchAll(g.out, sp)
		return frag{start: g.start, out: []patchRef{{sp, 1}}}, nil
	}
	c.insts[sp].y = uint32(g.start)
	c.patchAll(g.out, sp)
	return frag{start: g.start, out: []patchRef{{sp, 0}}}, nil
}

// compileQuest wraps an already-compiled fragment in an optional
// split.
func (c *compiler) compileQuest(g frag, greedy bool) (frag, error) {
	sp, err := c.emit(opSplit, 0)
	if err != nil {
		return frag{}, err
	}
	if greedy {
		c.insts[sp].x = uint32(g.start)
		return frag{start: sp, out: append(g.out, patchRef{sp, 1})}, nil
	}
	c.insts[sp].y = uint32(g.start)
	return frag{start: sp, out: append(g.out, patchRef{sp, 0})}, nil
}

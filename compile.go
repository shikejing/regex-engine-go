package regex

import "fmt"

// opCode 是编译产物中的指令操作码。
type opCode int

const (
	opChar  opCode = iota // 匹配单个 rune
	opAny                 // 匹配任意 rune（不含换行）
	opClass               // 匹配字符类
	opSplit               // 按优先级分叉：x 优先于 y
	opJmp                 // 无条件跳转
	opSave                // 记录捕获边界
	opMatch               // 接受
	opBol                 // 文本起始锚点 ^
	opEol                 // 文本结束锚点 $
	opRef                 // 反向引用（仅回溯引擎使用）
)

// inst 是一条编译后的指令。
type inst struct {
	op   opCode
	r    rune       // opChar
	cls  *charClass // opClass
	x, y int        // opSplit 分支 / opJmp 目标
	slot int        // opSave 槽位
	ref  int        // opRef 组号
}

// prog 是不可变的编译产物。
type prog struct {
	insts   []inst
	start   int
	numCaps int  // 捕获组数量（不含第 0 组整体匹配）
	hasRef  bool // 是否包含反向引用
}

// maxInsts 限制编译产物的指令数，防止 {n,m} 展开导致内存失控。
const maxInsts = 100000

// compiler 把 AST 编译为指令程序。
type compiler struct {
	insts []inst
}

// compile 把 AST 编译为指令程序。
func compile(root *node, numCaps int, hasRef bool) (*prog, error) {
	c := &compiler{}
	c.emitSave(0)
	if err := c.emitNode(root); err != nil {
		return nil, err
	}
	c.emitSave(1)
	c.emit(inst{op: opMatch})
	return &prog{
		insts:   c.insts,
		start:   0,
		numCaps: numCaps,
		hasRef:  hasRef,
	}, nil
}

func (c *compiler) emit(i inst) int {
	c.insts = append(c.insts, i)
	return len(c.insts) - 1
}

func (c *compiler) emitSave(slot int) {
	c.emit(inst{op: opSave, slot: slot})
}

// guard 检查程序规模上限。
func (c *compiler) guard() error {
	if len(c.insts) > maxInsts {
		return fmt.Errorf("regex: compiled program exceeds %d instructions", maxInsts)
	}
	return nil
}

func (c *compiler) emitNode(n *node) error {
	if err := c.guard(); err != nil {
		return err
	}
	switch n.kind {
	case kEmpty:
		return nil
	case kLiteral:
		c.emit(inst{op: opChar, r: n.r})
	case kDot:
		c.emit(inst{op: opAny})
	case kClass:
		c.emit(inst{op: opClass, cls: n.class})
	case kAnchor:
		if n.anchor == anchorStart {
			c.emit(inst{op: opBol})
		} else {
			c.emit(inst{op: opEol})
		}
	case kConcat:
		for _, k := range n.kids {
			if err := c.emitNode(k); err != nil {
				return err
			}
		}
	case kAlternate:
		// 链式分叉：b1 | b2 | ... | bn
		// 分支按书写顺序排列优先级，保证确定性语义。
		var jmpPatches []int
		for i, b := range n.kids {
			var sp int
			if i < len(n.kids)-1 {
				sp = c.emit(inst{op: opSplit, x: len(c.insts) + 1})
			}
			if err := c.emitNode(b); err != nil {
				return err
			}
			if i < len(n.kids)-1 {
				jmpPatches = append(jmpPatches, c.emit(inst{op: opJmp}))
				c.insts[sp].y = len(c.insts)
			}
		}
		for _, j := range jmpPatches {
			c.insts[j].x = len(c.insts)
		}
	case kCapture:
		c.emitSave(2 * n.capIndex)
		if err := c.emitNode(n.body); err != nil {
			return err
		}
		c.emitSave(2*n.capIndex + 1)
	case kBackref:
		c.emit(inst{op: opRef, ref: n.refIndex})
	case kRepeat:
		return c.emitRepeat(n)
	}
	return nil
}

// emitRepeat 通过展开编译量词：
//   - lo 次必选拷贝
//   - hi-lo 次可选拷贝（hi >= 0 时）
//   - hi < 0 时追加一个星号循环
func (c *compiler) emitRepeat(n *node) error {
	for i := 0; i < n.lo; i++ {
		if err := c.emitNode(n.sub); err != nil {
			return err
		}
	}
	if n.hi >= 0 {
		// 嵌套可选：q? q? ... q?（从外向内记录 End 补丁）
		var patches []int
		for i := n.lo; i < n.hi; i++ {
			var sp int
			if n.greedy {
				sp = c.emit(inst{op: opSplit, x: len(c.insts) + 1})
			} else {
				sp = c.emit(inst{op: opSplit, y: len(c.insts) + 1})
			}
			patches = append(patches, sp)
			if err := c.emitNode(n.sub); err != nil {
				return err
			}
		}
		end := len(c.insts)
		for _, sp := range patches {
			if n.greedy {
				c.insts[sp].y = end
			} else {
				c.insts[sp].x = end
			}
		}
		return nil
	}
	// 无上限：追加星号循环
	loop := len(c.insts)
	var sp int
	if n.greedy {
		sp = c.emit(inst{op: opSplit, x: len(c.insts) + 1})
	} else {
		sp = c.emit(inst{op: opSplit, y: len(c.insts) + 1})
	}
	if err := c.emitNode(n.sub); err != nil {
		return err
	}
	c.emit(inst{op: opJmp, x: loop})
	end := len(c.insts)
	if n.greedy {
		c.insts[sp].y = end
	} else {
		c.insts[sp].x = end
	}
	return nil
}

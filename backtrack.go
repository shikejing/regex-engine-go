package regex

import (
	"errors"
	"unicode/utf8"
)

// ErrBacktrackLimit 在回溯引擎执行步数超过预算时返回，
// 用于可区分地终止病态回溯模式，而不是卡死。
var ErrBacktrackLimit = errors.New("regex: backtrack step budget exceeded")

// defaultBacktrackBudget 是一次匹配允许的最大指令执行步数
// （所有候选起点共享）。预算同时限制了递归深度，
// 从而保证回溯引擎的时间与内存都有界。
const defaultBacktrackBudget = 1_000_000

// backtracker 是带回溯的执行引擎，仅在模式包含反向引用时使用。
// 语义与 Pike VM 一致：最左匹配、分支按书写顺序优先、
// 量词按贪婪/懒惰标记展开；超出步数预算时以 ErrBacktrackLimit 终止。
type backtracker struct {
	p      *prog
	budget int

	// 以下字段为单次 search 调用的私有状态，不跨调用共享。
	steps     int
	onPathPos []int // 每个 split pc 在当前递归路径上的输入位置，用于切断空循环
	input     string
}

// search 在 input 中查找最左匹配，返回捕获槽位；
// 超过预算时返回 ErrBacktrackLimit。
func (bt *backtracker) search(input string) ([]int, error) {
	bt.input = input
	bt.steps = bt.budget
	bt.onPathPos = make([]int, len(bt.p.insts))
	for i := range bt.onPathPos {
		bt.onPathPos[i] = -1
	}
	nslots := 2 * (bt.p.numCaps + 1)

	anchored := len(bt.p.insts) >= 2 &&
		bt.p.insts[0].op == opSave && bt.p.insts[1].op == opBol

	for start := 0; start <= len(input); {
		slots := make([]int, nslots)
		for i := range slots {
			slots[i] = -1
		}
		if got, ok, err := bt.run(bt.p.start, start, slots); err != nil {
			return nil, err
		} else if ok {
			return got, nil
		}
		if anchored {
			break
		}
		if start == len(input) {
			break
		}
		_, size := utf8.DecodeRuneInString(input[start:])
		start += size
	}
	return nil, nil
}

// run 从 (pc, pos) 继续执行，返回最终的捕获槽位。
// 通过尾递归延续（pc 直接推进）与显式递归（split 的优先分支）控制栈深。
func (bt *backtracker) run(pc, pos int, slots []int) ([]int, bool, error) {
	p := bt.p
	for {
		if bt.steps--; bt.steps < 0 {
			return nil, false, ErrBacktrackLimit
		}
		in := &p.insts[pc]
		switch in.op {
		case opChar:
			if pos >= len(bt.input) {
				return nil, false, nil
			}
			r, size := utf8.DecodeRuneInString(bt.input[pos:])
			if r != in.r {
				return nil, false, nil
			}
			pos += size
			pc++
		case opAny:
			if pos >= len(bt.input) {
				return nil, false, nil
			}
			r, size := utf8.DecodeRuneInString(bt.input[pos:])
			if r == '\n' {
				return nil, false, nil
			}
			pos += size
			pc++
		case opClass:
			if pos >= len(bt.input) {
				return nil, false, nil
			}
			r, size := utf8.DecodeRuneInString(bt.input[pos:])
			if !in.cls.contains(r) {
				return nil, false, nil
			}
			pos += size
			pc++
		case opBol:
			if pos != 0 {
				return nil, false, nil
			}
			pc++
		case opEol:
			if pos != len(bt.input) {
				return nil, false, nil
			}
			pc++
		case opJmp:
			pc = in.x
		case opSplit:
			// 空循环切断：同一 split 在同一输入位置已在递归路径上。
			if bt.onPathPos[pc] == pos {
				return nil, false, nil
			}
			old := bt.onPathPos[pc]
			bt.onPathPos[pc] = pos
			got, ok, err := bt.run(in.x, pos, slots)
			bt.onPathPos[pc] = old
			if err != nil || ok {
				return got, ok, err
			}
			pc = in.y
		case opSave:
			ns := make([]int, len(slots))
			copy(ns, slots)
			ns[in.slot] = pos
			slots = ns
			pc++
		case opRef:
			lo, hi := slots[2*in.ref], slots[2*in.ref+1]
			if lo < 0 || hi < 0 {
				// 未参与的捕获组：按匹配空串处理。
				pc++
				continue
			}
			want := bt.input[lo:hi]
			if pos+len(want) > len(bt.input) || bt.input[pos:pos+len(want)] != want {
				return nil, false, nil
			}
			pos += len(want)
			pc++
		case opMatch:
			return slots, true, nil
		}
	}
}

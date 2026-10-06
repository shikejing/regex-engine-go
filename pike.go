package regex

import "unicode/utf8"

// pikeVM 是 Thompson NFA 模拟器（Pike VM），
// 对不含反向引用的模式提供 O(len(input) * len(prog)) 的
// 最左、分支按书写顺序优先的确定性匹配语义。
//
// 未加锚点的搜索通过在每个输入位置向线程列表尾部播种新起点实现，
// 所有候选起点并行推进，因此不会退化成逐起点重扫的平方级开销。
type pikeVM struct {
	p *prog
}

// thread 是一条并发执行的 NFA 线程。
type thread struct {
	pc    int
	slots []int // 捕获槽位，2*(numCaps+1) 个，-1 表示未设置
}

// threadList 是一步内的线程集合，带代数标记去重。
type threadList struct {
	t    []thread
	mark []int // 每个 pc 的去重标记
	gen  int   // 当前代数
}

func newThreadList(n int) *threadList {
	// gen 从 1 开始，避免与 mark 的零值冲突。
	return &threadList{mark: make([]int, n), gen: 1}
}

func (l *threadList) reset() {
	l.gen++
	l.t = l.t[:0]
}

// search 在 input 中查找最左匹配，返回捕获槽位（2*(numCaps+1) 个），
// 未匹配时返回 nil。该函数不修改任何共享状态，可并发调用。
func (vm *pikeVM) search(input string) []int {
	p := vm.p
	nslots := 2 * (p.numCaps + 1)
	clist := newThreadList(len(p.insts))
	nlist := newThreadList(len(p.insts))

	var matched []int
	pos := 0
	for {
		// 尚未找到匹配时，在当前位置播种一个新起点线程（最低优先级）。
		if matched == nil {
			slots := make([]int, nslots)
			for i := range slots {
				slots[i] = -1
			}
			vm.addThread(clist, p.start, slots, input, pos)
		}
		if pos >= len(input) {
			// 输入结束：按优先级检查是否有线程处于接受态。
			for _, th := range clist.t {
				if p.insts[th.pc].op == opMatch {
					matched = th.slots
					break
				}
			}
			break
		}
		r, size := utf8.DecodeRuneInString(input[pos:])
		nlist.reset()
	step:
		for _, th := range clist.t {
			in := &p.insts[th.pc]
			switch in.op {
			case opChar:
				if r == in.r {
					vm.addThread(nlist, th.pc+1, th.slots, input, pos+size)
				}
			case opAny:
				if r != '\n' {
					vm.addThread(nlist, th.pc+1, th.slots, input, pos+size)
				}
			case opClass:
				if in.cls.contains(r) {
					vm.addThread(nlist, th.pc+1, th.slots, input, pos+size)
				}
			case opMatch:
				// 找到匹配：记录并截断本步内所有更低优先级的线程。
				// 列表按优先级有序，之后的匹配只会被更高优先级的
				// 线程（排在前面的）更新，保证语义确定。
				matched = th.slots
				break step
			}
		}
		clist, nlist = nlist, clist
		pos += size
	}
	return matched
}

// addThread 把 pc 及其 epsilon 闭包加入列表。
// 沿途解析 Save/Jmp/Split/锚点；遇到消费型指令或 Match 才真正入列。
func (vm *pikeVM) addThread(l *threadList, pc int, slots []int, input string, pos int) {
	if l.mark[pc] == l.gen {
		return // 本步已存在该状态的线程（也用于切断空循环）
	}
	l.mark[pc] = l.gen
	in := &vm.p.insts[pc]
	switch in.op {
	case opJmp:
		vm.addThread(l, in.x, slots, input, pos)
	case opSplit:
		// x 优先于 y，先递归 x 保证列表内优先级有序。
		vm.addThread(l, in.x, slots, input, pos)
		vm.addThread(l, in.y, slots, input, pos)
	case opSave:
		ns := make([]int, len(slots))
		copy(ns, slots)
		ns[in.slot] = pos
		vm.addThread(l, pc+1, ns, input, pos)
	case opBol:
		if pos == 0 {
			vm.addThread(l, pc+1, slots, input, pos)
		}
	case opEol:
		if pos == len(input) {
			vm.addThread(l, pc+1, slots, input, pos)
		}
	default:
		// opChar / opAny / opClass / opMatch / opRef
		l.t = append(l.t, thread{pc: pc, slots: slots})
	}
}

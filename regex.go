// Package regex 提供一套自包含的正则匹配能力，
// 适用于日志过滤、路由匹配与文本校验等场景。
//
// 编译与匹配完全在本包内实现，不依赖标准库 regexp 或第三方引擎。
// 语法子集、匹配语义与执行代价模型见仓库 README.md。
package regex

// Regexp 是编译后的正则表达式。
// 编译产物不可变，匹配过程不修改任何共享状态，
// 因此同一个 *Regexp 可被多个协程并发安全使用。
type Regexp struct {
	src  string
	prog *prog
}

// Compile 解析并编译模式串，返回可复用的 *Regexp。
// 语法错误或不支持的写法返回 *SyntaxError（携带出错位置）。
func Compile(src string) (*Regexp, error) {
	root, numCaps, hasRef, err := parse(src)
	if err != nil {
		return nil, err
	}
	prog, err := compile(root, numCaps, hasRef)
	if err != nil {
		return nil, err
	}
	return &Regexp{src: src, prog: prog}, nil
}

// MustCompile 类似 Compile，但出错时 panic。
func MustCompile(src string) *Regexp {
	re, err := Compile(src)
	if err != nil {
		panic(err)
	}
	return re
}

// String 返回原始模式串。
func (re *Regexp) String() string { return re.src }

// NumSubexp 返回捕获分组数量（不含整体匹配）。
func (re *Regexp) NumSubexp() int { return re.prog.numCaps }

// findSubmatch 是匹配的统一入口。
func (re *Regexp) findSubmatch(input string) ([]int, error) {
	if re.prog.hasRef {
		// 含反向引用：使用带步数预算的回溯引擎。
		bt := &backtracker{p: re.prog, budget: defaultBacktrackBudget}
		return bt.search(input)
	}
	// 普通模式：Pike VM，代价随输入规模线性增长。
	vm := &pikeVM{p: re.prog}
	return vm.search(input), nil
}

// Match 报告输入中是否存在匹配。
// 若模式触发回溯预算上限，返回 (false, ErrBacktrackLimit)，
// 与"无匹配"可明确区分。
func (re *Regexp) Match(input string) (bool, error) {
	slots, err := re.findSubmatch(input)
	if err != nil {
		return false, err
	}
	return slots != nil, nil
}

// Find 返回最左匹配的字节区间 [start, end)，未匹配返回 nil。
// 若模式触发回溯预算上限，matched 为 nil 且 err 为 ErrBacktrackLimit。
func (re *Regexp) Find(input string) (matched []int, err error) {
	slots, err := re.findSubmatch(input)
	if err != nil || slots == nil {
		return nil, err
	}
	return slots[:2], nil
}

// FindSubmatch 返回整体匹配与各捕获组的字节区间，
// 布局为 [m0s, m0e, g1s, g1e, ...]，未参与的组为 [-1, -1]。
// 未匹配返回 (nil, nil)。若模式触发回溯预算上限，
// 返回 (nil, ErrBacktrackLimit)。
func (re *Regexp) FindSubmatch(input string) ([]int, error) {
	return re.findSubmatch(input)
}

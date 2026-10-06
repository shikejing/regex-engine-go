package regex

// nodeKind 标识 AST 节点类型。
type nodeKind int

const (
	kEmpty     nodeKind = iota // 空节点（匹配空串）
	kLiteral                   // 单个字面量 rune
	kDot                       // 任意字符（默认不含换行）
	kClass                     // 字符类
	kAnchor                    // ^ 或 $
	kConcat                    // 顺序连接
	kAlternate                 // 选择分支 |
	kRepeat                    // 量词
	kCapture                   // 捕获分组
	kBackref                   // 反向引用 \1..\9
)

// node 是解析后的语法树节点，编译完成后只读。
type node struct {
	kind nodeKind

	// kLiteral
	r rune

	// kClass
	class *charClass

	// kAnchor
	anchor anchorKind

	// kConcat / kAlternate
	kids []*node

	// kRepeat
	lo, hi int // hi < 0 表示无上限
	greedy bool
	sub    *node

	// kCapture
	capIndex int // 捕获组编号，从 1 开始
	body     *node

	// kBackref
	refIndex int
}

// anchorKind 区分行首与行尾锚点。
type anchorKind int

const (
	anchorStart anchorKind = iota // ^
	anchorEnd                     // $
)

// charClass 表示一个字符类，用排序且不相交的 rune 区间表示。
type charClass struct {
	ranges []runeRange
}

// runeRange 是闭区间 [lo, hi]。
type runeRange struct {
	lo, hi rune
}

// contains 判断 r 是否在字符类内。ranges 已排序且不相交，使用二分查找。
func (c *charClass) contains(r rune) bool {
	lo, hi := 0, len(c.ranges)
	for lo < hi {
		mid := (lo + hi) / 2
		rg := c.ranges[mid]
		switch {
		case r < rg.lo:
			hi = mid
		case r > rg.hi:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

// normalize 把可能乱序/重叠的区间列表整理为排序且不相交的区间列表。
func normalizeRanges(rs []runeRange) []runeRange {
	if len(rs) == 0 {
		return nil
	}
	// 简单插入排序，字符类通常很小。
	sorted := make([]runeRange, len(rs))
	copy(sorted, rs)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].lo < sorted[j-1].lo; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	out := sorted[:0]
	for _, rg := range sorted {
		if n := len(out); n > 0 && rg.lo <= out[n-1].hi+1 {
			if rg.hi > out[n-1].hi {
				out[n-1].hi = rg.hi
			}
		} else {
			out = append(out, rg)
		}
	}
	return out
}

// negate 返回字符类在 Unicode 标量值域 [0, 0x10FFFF] 内的补集。
func (c *charClass) negate() *charClass {
	const maxRune = 0x10FFFF
	var out []runeRange
	next := rune(0)
	for _, rg := range c.ranges {
		if rg.lo > next {
			out = append(out, runeRange{next, rg.lo - 1})
		}
		if rg.hi >= next {
			next = rg.hi + 1
		}
	}
	if next <= maxRune {
		out = append(out, runeRange{next, maxRune})
	}
	return &charClass{ranges: out}
}

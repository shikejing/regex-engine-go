package regex

import (
	"errors"
	"strings"
	"testing"
)

// TestSyntaxErrors 验证非法模式在编译期报错，且错误携带位置信息。
func TestSyntaxErrors(t *testing.T) {
	cases := []struct {
		pat    string
		wantAt int // 期望的错误位置，-1 表示只检查报错不检查位置
	}{
		{`(`, 0},       // 未闭合分组
		{`(abc`, 0},    // 未闭合分组
		{`)`, 0},       // 多余右括号
		{`a)`, 1},      // 多余右括号
		{`[`, 0},       // 未闭合字符类
		{`[abc`, 0},    // 未闭合字符类
		{`[z-a]`, -1},  // 区间倒置
		{`*a`, 0},      // 量词无前置原子
		{`a{2,1}`, 1},  // 计数区间倒置
		{`a{`, 1},      // 非法计数形式
		{`a{1001}`, 1}, // 超过重复上限
		{`\`, 0},       // 结尾反斜杠
		{`\q`, 0},      // 不支持的转义
		{`a\`, 1},      // 结尾反斜杠
		{`\x1`, 0},     // 不完整的十六进制转义
		{`\xzz`, 0},    // 非法十六进制
		{`(?i)a`, -1},  // 不支持的分组修饰（本实现当作普通字符? 见下）
	}
	for _, tc := range cases {
		_, err := Compile(tc.pat)
		if err == nil {
			t.Errorf("Compile(%q): expected error, got nil", tc.pat)
			continue
		}
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("Compile(%q): error is %T, want *SyntaxError", tc.pat, err)
			continue
		}
		if tc.wantAt >= 0 && se.Pos != tc.wantAt {
			t.Errorf("Compile(%q): error pos = %d, want %d (err=%v)", tc.pat, se.Pos, tc.wantAt, err)
		}
		if se.Pos < 0 || se.Pos > len(tc.pat) {
			t.Errorf("Compile(%q): error pos %d out of range", tc.pat, se.Pos)
		}
	}
}

// TestErrorMessageContainsPos 验证错误消息可读且含位置。
func TestErrorMessageContainsPos(t *testing.T) {
	_, err := Compile(`ab(cd`)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "offset 2") {
		t.Fatalf("error message %q should mention offset 2", err.Error())
	}
}

// TestBackrefs 验证反向引用。
func TestBackrefs(t *testing.T) {
	cases := []struct {
		pat, input string
		want       string
		ok         bool
	}{
		{`(\w+) \1`, "hello hello world", "hello hello", true},
		{`(\w+) \1`, "hello world", "", false},
		{`(ab|cd)\1`, "xxcdcdyy", "cdcd", true},
		{`(ab|cd)\1`, "xxcdabyy", "", false},
		{`^(a+)\1$`, "aaaa", "aaaa", true}, // 2+2
		{`^(a+)\1$`, "aaaaa", "", false},   // 奇数长度不行
		{`(a)(b)\2\1`, "xabba", "abba", true},
		{`(\d+)-\1`, "12-12", "12-12", true},
		{`(\d+)-\1`, "12-13", "", false},
	}
	for _, tc := range cases {
		re := MustCompile(tc.pat)
		got, ok := findString(t, re, tc.input)
		if ok != tc.ok || got != tc.want {
			t.Errorf("pat=%q input=%q: got (%q,%v), want (%q,%v)",
				tc.pat, tc.input, got, ok, tc.want, tc.ok)
		}
	}
}

// TestBackrefCaptures 验证含反向引用模式的捕获边界。
func TestBackrefCaptures(t *testing.T) {
	re := MustCompile(`(\w+) \1`)
	slots, err := re.FindSubmatch("say hi hi!")
	if err != nil {
		t.Fatal(err)
	}
	if slots == nil {
		t.Fatal("no match")
	}
	if got := "say hi hi!"[slots[2]:slots[3]]; got != "hi" {
		t.Fatalf("group1 = %q, want %q", got, "hi")
	}
}

// TestUnmatchedGroupBackref 未参与的捕获组的反引用按空串处理。
func TestUnmatchedGroupBackref(t *testing.T) {
	re := MustCompile(`(a)?b\1`)
	if !mustMatch(t, re, "b") {
		t.Fatal(`(a)?b\1 should match "b" (unset group matches empty)`)
	}
	if !mustMatch(t, re, "aba") {
		t.Fatal(`(a)?b\1 should match "aba"`)
	}
}

// TestStackedQuantifiers 量词叠加是合法的（与 RE2 一致），语义为嵌套重复。
func TestStackedQuantifiers(t *testing.T) {
	re := MustCompile(`a**`)
	if !mustMatch(t, re, "aaa") || !mustMatch(t, re, "") {
		t.Fatal("a** should match any run of a's including empty")
	}
	re2 := MustCompile(`(a*)*`)
	got, ok := findString(t, re2, "aab")
	if !ok || got != "aa" {
		t.Fatalf("(a*)* on %q: got %q", "aab", got)
	}
}

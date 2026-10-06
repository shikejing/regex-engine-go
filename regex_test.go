package regex

import (
	"regexp"
	"testing"
)

// findString 返回最左匹配子串，未匹配返回空串与 false。
func findString(t *testing.T, re *Regexp, input string) (string, bool) {
	t.Helper()
	loc, err := re.Find(input)
	if err != nil {
		t.Fatalf("Find(%q) error: %v", input, err)
	}
	if loc == nil {
		return "", false
	}
	return input[loc[0]:loc[1]], true
}

// TestAgainstStdlib 用标准库 regexp 作为参照（仅测试代码使用），
// 验证一批模式的最左匹配与整体边界一致。
func TestAgainstStdlib(t *testing.T) {
	cases := []struct {
		pat   string
		input string
	}{
		{`abc`, "xxabcyy"},
		{`a+`, "aaab"},
		{`a+?`, "aaab"},
		{`a*`, "bbb"},
		{`a*?`, "aaa"},
		{`ab|a`, "xab"},
		{`a|ab`, "xab"},
		{`(a|b)*c`, "ababcc"},
		{`(ab|a)(b?)`, "zab"},
		{`a{2,4}`, "aaaaa"},
		{`a{2,4}?`, "aaaaa"},
		{`a{2,}`, "aaaaa"},
		{`a{3}`, "aaaa"},
		{`(a{2}){2}`, "aaaa"},
		{`[a-c]+`, "zabcz"},
		{`[^a-c]+`, "abcXYZabc"},
		{`\d+`, "abc123def"},
		{`\w+`, "  hello_42  "},
		{`\s\w`, " \tx"},
		{`a.c`, "xabcy"},
		{`^abc`, "abcdef"},
		{`abc$`, "xxabc"},
		{`^a+$`, "aaaa"},
		{`(a*)(a*)`, "aaa"},
		{`(a+)(a+)`, "aaa"},
		{`(a|ab)(c|bcd)`, "abcd"},
		{`x?y`, "y"},
		{`(foo|foobar|fo)+`, "foobarfoo"},
		{`é+`, "ööééé"},
		{`[α-ω]+`, "123αγωabc"},
		{`a??a`, "a"},
		{`(a?)*b`, "aab"},
		{``, "abc"},
		{`a|`, "ba"},
		{`|a`, "ba"},
		{`(())`, "x"},
		{`((((a))))`, "xa"},
	}
	for _, tc := range cases {
		re, err := Compile(tc.pat)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tc.pat, err)
		}
		got, ok := findString(t, re, tc.input)
		std := regexp.MustCompile(tc.pat)
		loc := std.FindStringIndex(tc.input)
		wantOK := loc != nil
		var want string
		if wantOK {
			want = tc.input[loc[0]:loc[1]]
		}
		if ok != wantOK || got != want {
			t.Errorf("pat=%q input=%q: got (%q,%v), stdlib wants (%q,%v)",
				tc.pat, tc.input, got, ok, want, wantOK)
		}
	}
}

// TestSubmatchAgainstStdlib 对比捕获分组边界。
func TestSubmatchAgainstStdlib(t *testing.T) {
	cases := []struct {
		pat   string
		input string
	}{
		{`(a)(b)?`, "xa"},
		{`(a)(b)?`, "xab"},
		{`(a+)(b+)`, "aabb"},
		{`(a+?)(b+)`, "aabb"},
		{`((a)|b)*`, "ab"},
		{`(a|b)*c`, "abab"},
		{`(a)(b)(c)?`, "ab"},
		{`(\d+)-(\d+)`, "call 555-1234 now"},
		{`(a(b(c)))`, "abc"},
		{`(?:a(b))+`, "abab"},
		{`(x)?(y)`, "y"},
		{`(a?)*`, "aa"},
	}
	for _, tc := range cases {
		re := MustCompile(tc.pat)
		got, err := re.FindSubmatch(tc.input)
		if err != nil {
			t.Fatalf("FindSubmatch(%q): %v", tc.input, err)
		}
		std := regexp.MustCompile(tc.pat)
		want := std.FindStringSubmatchIndex(tc.input)
		if (got == nil) != (want == nil) {
			t.Fatalf("pat=%q input=%q: got %v, want %v", tc.pat, tc.input, got, want)
		}
		if got == nil {
			continue
		}
		if len(got) != len(want) {
			t.Fatalf("pat=%q: got %d slots, want %d", tc.pat, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("pat=%q input=%q slot[%d]: got %d, want %d (got=%v want=%v)",
					tc.pat, tc.input, i, got[i], want[i], got, want)
			}
		}
	}
}

// TestGreedyLazy 显式验证贪婪与懒惰语义。
func TestGreedyLazy(t *testing.T) {
	cases := []struct {
		pat, input, want string
	}{
		{`<.+>`, "<a><b>", "<a><b>"}, // 贪婪
		{`<.+?>`, "<a><b>", "<a>"},   // 懒惰
		{`a{2,4}`, "aaaaa", "aaaa"},
		{`a{2,4}?`, "aaaaa", "aa"},
		{`"[^"]*"`, `"x" "y"`, `"x"`},
	}
	for _, tc := range cases {
		re := MustCompile(tc.pat)
		got, ok := findString(t, re, tc.input)
		if !ok || got != tc.want {
			t.Errorf("pat=%q input=%q: got %q, want %q", tc.pat, tc.input, got, tc.want)
		}
	}
}

// TestLeftmost 验证最左匹配：即使更靠右的匹配更长，也取最左。
func TestLeftmost(t *testing.T) {
	re := MustCompile(`a+`)
	loc, err := re.Find("aa bb aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if loc[0] != 0 || loc[1] != 2 {
		t.Fatalf("got %v, want [0 2]", loc)
	}
	// 分支优先级：书写在前的分支优先，即使它更短。
	re2 := MustCompile(`a|ab`)
	got, _ := findString(t, re2, "ab")
	if got != "a" {
		t.Fatalf("got %q, want %q", got, "a")
	}
}

// mustMatch 断言 Match 无错误并返回匹配结果。
func mustMatch(t *testing.T, re *Regexp, input string) bool {
	t.Helper()
	ok, err := re.Match(input)
	if err != nil {
		t.Fatalf("Match(%q) error: %v", input, err)
	}
	return ok
}

// TestAnchors 验证锚点语义（文本起始/结束）。
func TestAnchors(t *testing.T) {
	if !mustMatch(t, MustCompile(`^foo`), "foobar") {
		t.Error("^foo should match foobar")
	}
	if mustMatch(t, MustCompile(`^foo`), "barfoo") {
		t.Error("^foo should not match barfoo")
	}
	if !mustMatch(t, MustCompile(`foo$`), "barfoo") {
		t.Error("foo$ should match barfoo")
	}
	if mustMatch(t, MustCompile(`foo$`), "foobar") {
		t.Error("foo$ should not match foobar")
	}
	if !mustMatch(t, MustCompile(`^$`), "") {
		t.Error("^$ should match empty string")
	}
	// 多行输入：^/$ 是文本锚点，不是行锚点。
	if mustMatch(t, MustCompile(`^bar`), "foo\nbar") {
		t.Error("^bar should not match across newline")
	}
}

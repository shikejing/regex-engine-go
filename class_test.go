package regex

import "testing"

// TestCharClasses 字符类、区间、取反与类内转义。
func TestCharClasses(t *testing.T) {
	cases := []struct {
		pat, input string
		want       string
		ok         bool
	}{
		{`[abc]+`, "zabca", "abca", true},
		{`[a-z]+`, "9abcZ", "abc", true},
		{`[^a-z]+`, "abc123", "123", true},
		{`[]a]+`, "b]a]", "]a]", true}, // ']' 作为首字符是字面量
		{`[a-]+`, "b-a-", "-a-", true}, // 结尾 '-' 是字面量
		{`[-a]+`, "b-a", "-a", true},   // 开头 '-' 是字面量
		{`[\d]+`, "ab12", "12", true},  // 类内 \d
		{`[\w.]+`, " a.b_1 ", "a.b_1", true},
		{`[^\d]+`, "12ab34", "ab", true},  // 取反类
		{`[\]]+`, "a]]b", "]]", true},     // 转义的 ]
		{`[\\]+`, "a\\\\b", "\\\\", true}, // 转义的反斜杠
		{`[\x41-\x5a]+`, "abAB", "AB", true},
		{`[^]a]+`, "]ab]", "b", true}, // 取反 + 首字符 ']'
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

// TestEscapes 原子位置的转义。
func TestEscapes(t *testing.T) {
	cases := []struct {
		pat, input string
		want       string
		ok         bool
	}{
		{`a\nb`, "xa\nby", "a\nb", true},
		{`a\tb`, "a\tb", "a\tb", true},
		{`\.`, "a.b", ".", true},
		{`\*\+`, "x*+y", "*+", true},
		{`\x41+`, "bAAc", "AA", true},
		{`A+`, "bAAc", "AA", true},
		{`\|`, "a|b", "|", true},
		{`\(`, "a(b", "(", true},
		{`\D+`, "12ab34", "ab", true},
		{`\S+`, "  abc def", "abc", true},
		{`\W+`, "ab!@cd", "!@", true},
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

// TestDotNewline 点号不匹配换行。
func TestDotNewline(t *testing.T) {
	re := MustCompile(`a.b`)
	if mustMatch(t, re, "a\nb") {
		t.Fatal("dot should not match newline")
	}
	if !mustMatch(t, re, "axb") {
		t.Fatal("dot should match 'x'")
	}
}

// TestEmptyPattern 空模式与空分支。
func TestEmptyPattern(t *testing.T) {
	re := MustCompile(``)
	loc, err := re.Find("abc")
	if err != nil || loc == nil {
		t.Fatalf("loc=%v err=%v", loc, err)
	}
	if loc[0] != 0 || loc[1] != 0 {
		t.Fatalf("empty pattern should match empty at 0, got %v", loc)
	}
	re2 := MustCompile(`a(|b)c`)
	if !mustMatch(t, re2, "ac") || !mustMatch(t, re2, "abc") {
		t.Fatal("empty alternative should work")
	}
}

// TestUnicode 多字节字符按 rune 匹配，区间按字节偏移返回。
func TestUnicode(t *testing.T) {
	re := MustCompile(`é+`)
	loc, err := re.Find("ööéééxx")
	if err != nil || loc == nil {
		t.Fatalf("loc=%v err=%v", loc, err)
	}
	if got := "ööéééxx"[loc[0]:loc[1]]; got != "ééé" {
		t.Fatalf("got %q", got)
	}
	if loc[0] != 4 { // 两个 ö 各占 2 字节
		t.Fatalf("start = %d, want 4", loc[0])
	}
}

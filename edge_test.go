package regex

import "testing"

func TestEdgeCases(t *testing.T) {
	// Program size limit.
	if _, err := Compile("(?:a{500}){500}"); err == nil {
		t.Error("expected program-too-large error")
	}
	// {0,0} matches empty.
	re := MustCompile("a{0,0}")
	ok, err := re.MatchString("aaa")
	if err != nil || !ok {
		t.Errorf("a{0,0}: %v %v", ok, err)
	}
	// Nested nullable loops terminate.
	for _, pat := range []string{"(a*)*b", "(a*)+b", "(a?)*b", "((a)*)*c", "(a|)*b"} {
		re := MustCompile(pat)
		if _, err := re.MatchString("aaab"); err != nil {
			t.Errorf("%q: %v", pat, err)
		}
		if _, err := re.MatchString("aaa"); err != nil {
			t.Errorf("%q: %v", pat, err)
		}
	}
	// Backref to a group inside a quantifier.
	re = MustCompile(`(ab|cd)\1`)
	m, err := re.FindStringSubmatch("xcdcdy")
	if err != nil || len(m) != 2 || m[0] != "cdcd" {
		t.Errorf("got %v %v", m, err)
	}
	// Backref to non-participating group fails that alternative.
	re = MustCompile(`(a)|(b)\2`)
	m, err = re.FindStringSubmatch("b")
	if err != nil {
		t.Fatal(err)
	}
	if m != nil {
		t.Errorf("expected no match, got %q", m)
	}
	// Empty alternation branches.
	re = MustCompile("a(|b)c")
	m, err = re.FindStringSubmatch("ac")
	if err != nil || m == nil {
		t.Errorf("got %v %v", m, err)
	}
	// Class edge cases.
	re = MustCompile("[^]]+")
	m, err = re.FindStringSubmatch("ab]cd")
	if err != nil || m == nil || m[0] != "ab" {
		t.Errorf("got %v %v", m, err)
	}
	// Dot does not match newline.
	re = MustCompile("a.c")
	ok, err = re.MatchString("a\nc")
	if err != nil || ok {
		t.Errorf("dot matched newline: %v %v", ok, err)
	}
}

package regex

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// mustFind compiles pat and returns FindStringSubmatch results.
func mustFind(t *testing.T, pat, s string) []string {
	t.Helper()
	re, err := Compile(pat)
	if err != nil {
		t.Fatalf("Compile(%q): %v", pat, err)
	}
	got, err := re.FindStringSubmatch(s)
	if err != nil {
		t.Fatalf("FindStringSubmatch(%q, %q): %v", pat, s, err)
	}
	return got
}

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGreedyAndLazy(t *testing.T) {
	cases := []struct {
		pat, s string
		want   []string
	}{
		// Greedy quantifiers take as much as possible.
		{"a+", "caaab", []string{"aaa"}},
		{"a*", "aaa", []string{"aaa"}},
		{"(ab)+", "zabababz", []string{"ababab", "ab"}},
		{"a{2,3}", "aaaaa", []string{"aaa"}},
		{"a{2,}", "aaaaa", []string{"aaaaa"}},
		{"(a|b)*c", "abababc", []string{"abababc", "b"}},
		// Lazy quantifiers take as little as possible.
		{"a+?", "caaab", []string{"a"}},
		{"a*?", "aaa", []string{""}},
		{"a??", "a", []string{""}},
		{"a{2,3}?", "aaaaa", []string{"aa"}},
		{"a{2,}?", "aaaaa", []string{"aa"}},
		{"<(.+?)>", "<a><b>", []string{"<a>", "a"}},
		{"<(.+)>", "<a><b>", []string{"<a><b>", "a><b"}},
		// Greedy still backtracks (via NFA priorities) to allow a match.
		{"a*ab", "aaab", []string{"aaab"}},
		{"(a*)(a*)", "aaa", []string{"aaa", "aaa", ""}},
		{"(a*?)(a*)", "aaa", []string{"aaa", "", "aaa"}},
	}
	for _, c := range cases {
		if got := mustFind(t, c.pat, c.s); !eqStrs(got, c.want) {
			t.Errorf("pattern %q on %q: got %q, want %q", c.pat, c.s, got, c.want)
		}
	}
}

func TestLeftmost(t *testing.T) {
	cases := []struct {
		pat, s string
		want   []string
	}{
		// Leftmost match wins even when a later match is longer.
		{"a+", "aa bb aa", []string{"aa"}},
		{"sam|same", "same sam", []string{"sam"}},
		{"same|sam", "same sam", []string{"same"}},
		// Alternation prefers the earlier branch at the same start.
		{"a|ab", "xab", []string{"a"}},
		{"ab|a", "xab", []string{"ab"}},
		// Empty match is found at the leftmost position.
		{"a*", "bbb", []string{""}},
		{"b*", "abc", []string{""}},
	}
	for _, c := range cases {
		if got := mustFind(t, c.pat, c.s); !eqStrs(got, c.want) {
			t.Errorf("pattern %q on %q: got %q, want %q", c.pat, c.s, got, c.want)
		}
	}
}

func TestGroupsAndClasses(t *testing.T) {
	cases := []struct {
		pat, s string
		want   []string
	}{
		{"(foo|bar)+", "xfoobarz", []string{"foobar", "bar"}},
		{"(?:foo|bar)+", "xfoobarz", []string{"foobar"}},
		{"(a)(b)(c)", "zabcz", []string{"abc", "a", "b", "c"}},
		{"((a)b)c", "zabc", []string{"abc", "ab", "a"}},
		{"[a-c]+", "zabcz", []string{"abc"}},
		{"[^a-c]+", "abcXYZabc", []string{"XYZ"}},
		{"[a-]+", "b-a-c", []string{"-a-"}},
		{"[]a]+", "a]b", []string{"a]"}},
		{`\d+`, "abc123", []string{"123"}},
		{`\w+`, "  foo_1 ", []string{"foo_1"}},
		{`\s\w+`, "  foo", []string{" foo"}},
		{`\D+`, "12ab34", []string{"ab"}},
		{"a.c", "xabcy", []string{"abc"}},
		{"a\\.c", "xa.cy", []string{"a.c"}},
		{"\\x41+", "zAAz", []string{"AA"}},
		{"\\u0042", "zBz", []string{"B"}},
	}
	for _, c := range cases {
		if got := mustFind(t, c.pat, c.s); !eqStrs(got, c.want) {
			t.Errorf("pattern %q on %q: got %q, want %q", c.pat, c.s, got, c.want)
		}
	}
}

func TestAnchors(t *testing.T) {
	cases := []struct {
		pat, s string
		want   []string
	}{
		{"^foo", "foobar", []string{"foo"}},
		{"foo$", "barfoo", []string{"foo"}},
		{"^foo$", "foo", []string{"foo"}},
		{"^b", "a\nb", []string{"b"}}, // ^ is a line anchor
		{"a$", "a\nb", []string{"a"}}, // $ is a line anchor
		{`\Afoo`, "foobar", []string{"foo"}},
		{`foo\z`, "barfoo", []string{"foo"}},
		{`\bfoo\b`, "a foo bar", []string{"foo"}},
		{`\Boo`, "foo", []string{"oo"}},
	}
	for _, c := range cases {
		if got := mustFind(t, c.pat, c.s); !eqStrs(got, c.want) {
			t.Errorf("pattern %q on %q: got %q, want %q", c.pat, c.s, got, c.want)
		}
	}
	// Anchored matches must not be found elsewhere.
	for _, c := range []struct{ pat, s string }{
		{"^foo", "xfoo"},
		{`foo\z`, "foox"},
		{`\Afoo`, "xfoo"},
	} {
		re := MustCompile(c.pat)
		ok, err := re.MatchString(c.s)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Errorf("pattern %q unexpectedly matched %q", c.pat, c.s)
		}
	}
}

func TestUnmatchedGroups(t *testing.T) {
	re := MustCompile("(a)|(b)")
	idx, err := re.FindStringSubmatchIndex("b")
	if err != nil {
		t.Fatal(err)
	}
	// Group 1 did not participate: [-1,-1]. Group 2 matched at [0,1].
	want := []int{0, 1, -1, -1, 0, 1}
	if !eqInts(idx, want) {
		t.Fatalf("got %v want %v", idx, want)
	}
}

func TestCompileErrors(t *testing.T) {
	bad := []string{
		"(",           // unclosed group
		")",           // unmatched close
		"a)",          // unmatched close
		"[",           // unclosed class
		"[a",          // unclosed class
		"[z-a]",       // invalid range
		"*a",          // nothing to repeat
		"+a",          // nothing to repeat
		"a**",         // nested repetition
		"a*+",         // nested repetition
		"a{2,1}",      // inverted range
		"a{",          // malformed counted repetition
		"a{1,2",       // unclosed counted repetition
		"a{1001}",     // count too large
		"a{0,2000}",   // count too large
		"(?i)a",       // unsupported flags
		"(?<x>a)",     // unsupported named group
		"a\\",         // trailing backslash
		"\\q",         // unknown escape
		"\\1",         // backreference to missing group
		"(a)\\2",      // backreference to missing group
		"a{999}{999}", // program too large
	}
	for _, pat := range bad {
		re, err := Compile(pat)
		if err == nil {
			t.Errorf("Compile(%q) unexpectedly succeeded", pat)
			continue
		}
		_ = re
		var synErr *Error
		if !errors.As(err, &synErr) {
			t.Errorf("Compile(%q): error %v is not a *Error", pat, err)
		}
	}
	// Error position is reported.
	_, err := Compile("ab(")
	var synErr *Error
	if !errors.As(err, &synErr) {
		t.Fatalf("expected *Error, got %v", err)
	}
	if synErr.Pos != 2 {
		t.Fatalf("error position = %d, want 2", synErr.Pos)
	}
}

func TestBackrefs(t *testing.T) {
	cases := []struct {
		pat, s string
		want   []string
	}{
		{`(ab)\1`, "xababy", []string{"abab", "ab"}},
		{`(\w+) \1`, "no no no", []string{"no no", "no"}},
		{`(a|b)\1`, "xaa", []string{"aa", "a"}},
		{`(a|b)\1`, "xab", nil}, // "ab" does not match: b != a
		{`(a*)(a*)\1`, "aaaa", []string{"aaaa", "aa", ""}},
	}
	for _, c := range cases {
		got := mustFind(t, c.pat, c.s)
		if c.want == nil {
			if got != nil {
				t.Errorf("pattern %q on %q: got %q, want no match", c.pat, c.s, got)
			}
			continue
		}
		if !eqStrs(got, c.want) {
			t.Errorf("pattern %q on %q: got %q, want %q", c.pat, c.s, got, c.want)
		}
	}
}

func TestBacktrackLimit(t *testing.T) {
	// (a|aa)+\1 needs exponential backtracking on a non-matching
	// input; the search must terminate with ErrBacktrackLimit
	// instead of running forever or returning a wrong answer.
	re, err := CompileWithOptions(`(a|aa)+\1$`, Options{BacktrackLimit: 100_000})
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Repeat("a", 40) + "b"
	done := make(chan error, 1)
	go func() {
		_, err := re.MatchString(s)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBacktrackLimit) {
			t.Fatalf("got %v, want ErrBacktrackLimit", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pathological match did not terminate")
	}
}

func TestPathologicalLinear(t *testing.T) {
	// Patterns that are exponential for naive backtrackers must run
	// in linear time here because they contain no backreferences.
	pats := []string{
		"(a+)+$",
		"(a|a)*$",
		"(a|aa)+$",
		"a?" + strings.Repeat("a?", 20) + strings.Repeat("a", 20),
	}
	s := strings.Repeat("a", 100) + "b"
	for _, pat := range pats {
		re := MustCompile(pat)
		done := make(chan bool, 1)
		go func() {
			ok, err := re.MatchString(s)
			if err != nil {
				t.Errorf("MatchString(%q): %v", pat, err)
			}
			done <- ok
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("pattern %q took too long", pat)
		}
	}
}

func TestLargeInput(t *testing.T) {
	// Matching must scale linearly with input size and must not
	// require quadratic rescans for unanchored search.
	re := MustCompile(`ERROR: [a-z]+ \d+`)
	s := strings.Repeat("ok ok ok ", 200_000) + "ERROR: disk 42" + strings.Repeat(" ok", 100_000)
	done := make(chan []string, 1)
	go func() {
		got, err := re.FindStringSubmatch(s)
		if err != nil {
			t.Errorf("match: %v", err)
		}
		done <- got
	}()
	select {
	case got := <-done:
		if len(got) != 1 || got[0] != "ERROR: disk 42" {
			t.Fatalf("got %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("large input match took too long")
	}
}

func TestConcurrentReuse(t *testing.T) {
	re := MustCompile(`(\w+)@(\w+)\.com`)
	inputs := []string{
		"mail me at alice@example.com please",
		"bob@test.com",
		"no address here",
		"carol@x.com and dave@y.com",
	}
	want := [][]string{
		{"alice@example.com", "alice", "example"},
		{"bob@test.com", "bob", "test"},
		nil,
		{"carol@x.com", "carol", "x"},
	}
	var wg sync.WaitGroup
	for k := 0; k < 32; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				j := (k + i) % len(inputs)
				got, err := re.FindStringSubmatch(inputs[j])
				if err != nil {
					t.Error(err)
					return
				}
				if !eqStrs(got, want[j]) {
					t.Errorf("input %q: got %q want %q", inputs[j], got, want[j])
					return
				}
			}
		}(k)
	}
	wg.Wait()
}

func TestDeterminism(t *testing.T) {
	// The same pattern and input must give identical results and
	// group boundaries on every run.
	re := MustCompile(`(a|ab)(c|bcd)(d*)`)
	s := "abcd"
	first, err := re.FindStringSubmatchIndex(s)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		got, err := re.FindStringSubmatchIndex(s)
		if err != nil {
			t.Fatal(err)
		}
		if !eqInts(got, first) {
			t.Fatalf("run %d: got %v, want %v", i, got, first)
		}
	}
}

func TestEmptyPattern(t *testing.T) {
	re := MustCompile("")
	idx, err := re.FindStringIndex("abc")
	if err != nil {
		t.Fatal(err)
	}
	if !eqInts(idx, []int{0, 0}) {
		t.Fatalf("got %v", idx)
	}
}

func TestUnicode(t *testing.T) {
	re := MustCompile("h.llo")
	got, ok, err := re.FindString("say héllo!")
	if err != nil || !ok {
		t.Fatalf("got %v %v", got, err)
	}
	m, err := re.FindStringSubmatch("say héllo!")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m[0] != "héllo" {
		t.Fatalf("got %q", m)
	}
	// Byte offsets must be correct around multi-byte runes.
	idx, err := MustCompile("llo").FindStringIndex("héllo")
	if err != nil {
		t.Fatal(err)
	}
	if !eqInts(idx, []int{3, 6}) {
		t.Fatalf("got %v", idx)
	}
}

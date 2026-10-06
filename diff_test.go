package regex

import (
	"math/rand"
	stdre "regexp"
	"testing"
)

// diff_test.go cross-checks the engine's observable semantics
// (leftmost-first match and capture boundaries) against the Go
// standard library, which implements the same leftmost-first
// semantics for this syntax subset. The engine itself never
// delegates matching to the standard library; this is only a test
// oracle. It also checks that the Pike VM and the backtracking VM
// agree with each other on backreference-free patterns.

// genPattern generates random patterns within the supported syntax
// subset.
func genPattern(r *rand.Rand, depth int) string {
	if depth <= 0 {
		switch r.Intn(6) {
		case 0:
			return string(rune('a' + r.Intn(3)))
		case 1:
			return "."
		case 2:
			return []string{"[a-c]", "[^a-c]", "\\d", "\\w", "\\s", "[ab-]", "[-b]"}[r.Intn(7)]
		case 3:
			return []string{"^", "$", "\\b", "\\B", "\\A", "\\z"}[r.Intn(6)]
		default:
			return string(rune('a' + r.Intn(3)))
		}
	}
	switch r.Intn(6) {
	case 0: // concat
		return genPattern(r, depth-1) + genPattern(r, depth-1)
	case 1: // alternation
		return "(" + genPattern(r, depth-1) + "|" + genPattern(r, depth-1) + ")"
	case 2: // group
		if r.Intn(2) == 0 {
			return "(" + genPattern(r, depth-1) + ")"
		}
		return "(?:" + genPattern(r, depth-1) + ")"
	case 3: // repeat
		q := []string{"*", "+", "?", "*?", "+?", "??", "{1,2}", "{0,2}?", "{2}", "{2,}", "{1,3}?"}[r.Intn(11)]
		return "(?:" + genPattern(r, depth-1) + ")" + q
	default:
		return genPattern(r, depth-1) + genPattern(r, depth-1)
	}
}

func genInput(r *rand.Rand) string {
	n := r.Intn(14)
	b := make([]byte, n)
	alphabet := "abcd01_ \n"
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}

func eqInts(a, b []int) bool {
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

func TestDifferential(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for i := 0; i < 4000; i++ {
		pat := genPattern(r, 3)
		re, err := Compile(pat)
		if err != nil {
			t.Fatalf("our Compile(%q): %v", pat, err)
		}
		// (?m) makes Go's ^ and $ line anchors, matching our
		// semantics.
		gre, err := stdre.Compile("(?m)" + pat)
		if err != nil {
			t.Fatalf("go Compile(%q): %v", pat, err)
		}
		for j := 0; j < 6; j++ {
			s := genInput(r)
			got, err := re.FindStringSubmatchIndex(s)
			if err != nil {
				t.Fatalf("our match (%q on %q): %v", pat, s, err)
			}
			want := gre.FindStringSubmatchIndex(s)
			if !eqInts(got, want) {
				t.Fatalf("pattern %q input %q: got %v want %v", pat, s, got, want)
			}
			// The two VMs must agree on backreference-free
			// patterns.
			bt, exceeded := backtrackSearch(re.prog, s, false, 5_000_000)
			if exceeded {
				t.Fatalf("backtracker exceeded budget (%q on %q)", pat, s)
			}
			if !eqInts(got, bt) {
				t.Fatalf("VM mismatch pattern %q input %q: pike %v backtrack %v", pat, s, got, bt)
			}
		}
	}
}

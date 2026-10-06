// Package regex implements a self-contained regular expression
// engine. Patterns are compiled into immutable programs that can be
// shared by any number of goroutines, and all matching is performed
// by engines implemented in this repository: a linear-time Thompson
// NFA simulation for ordinary patterns, and a budget-bounded
// backtracking VM for patterns that use backreferences. Neither the
// standard library regexp package nor any third-party engine is
// used.
package regex

import "errors"

// ErrBacktrackLimit is returned when a pattern that requires
// backtracking (one containing backreferences) exceeds its step
// budget during a match. The outcome is reported as an error rather
// than as a (possibly wrong) match or non-match, so callers can
// distinguish "no match" from "too expensive to decide".
var ErrBacktrackLimit = errors.New("regex: backtrack step limit exceeded")

// DefaultBacktrackLimit bounds the number of backtracking steps a
// single search may perform when the pattern contains
// backreferences.
const DefaultBacktrackLimit = 1_000_000

// Options configures compilation.
type Options struct {
	// BacktrackLimit bounds backtracking steps for patterns that
	// contain backreferences. Zero means DefaultBacktrackLimit.
	BacktrackLimit int
}

// Regexp is a compiled regular expression. It is immutable after
// Compile returns and safe for concurrent use by multiple
// goroutines; every match runs against private per-call state.
type Regexp struct {
	src            string
	prog           *prog
	backtrackLimit int
}

// Compile parses and compiles pattern with default options.
func Compile(pattern string) (*Regexp, error) {
	return CompileWithOptions(pattern, Options{})
}

// CompileWithOptions parses and compiles pattern. Syntax errors are
// reported as *Error with the byte offset of the problem.
func CompileWithOptions(pattern string, opts Options) (*Regexp, error) {
	root, numCap, err := parse(pattern)
	if err != nil {
		return nil, err
	}
	p, err := compileProgram(root, numCap)
	if err != nil {
		return nil, err
	}
	limit := opts.BacktrackLimit
	if limit <= 0 {
		limit = DefaultBacktrackLimit
	}
	return &Regexp{src: pattern, prog: p, backtrackLimit: limit}, nil
}

// MustCompile is like Compile but panics on error.
func MustCompile(pattern string) *Regexp {
	re, err := Compile(pattern)
	if err != nil {
		panic(err)
	}
	return re
}

// String returns the source pattern.
func (re *Regexp) String() string { return re.src }

// NumSubexp returns the number of capture groups, excluding group 0
// (the whole match).
func (re *Regexp) NumSubexp() int { return re.prog.numCap - 1 }

// search executes one leftmost search and returns the capture slots.
func (re *Regexp) search(s string) ([]int, error) {
	if re.prog.backrefs {
		caps, exceeded := backtrackSearch(re.prog, s, false, re.backtrackLimit)
		if exceeded {
			return nil, ErrBacktrackLimit
		}
		return caps, nil
	}
	return pikeSearch(re.prog, s, false), nil
}

// MatchString reports whether s contains any match of the pattern.
func (re *Regexp) MatchString(s string) (bool, error) {
	caps, err := re.search(s)
	if err != nil {
		return false, err
	}
	return caps != nil, nil
}

// FindStringIndex returns the [start, end) byte bounds of the
// leftmost match in s, or nil if there is no match.
func (re *Regexp) FindStringIndex(s string) ([]int, error) {
	caps, err := re.search(s)
	if err != nil || caps == nil {
		return nil, err
	}
	return caps[:2], nil
}

// FindString returns the text of the leftmost match, or "" if there
// is none. The boolean reports whether a match was found.
func (re *Regexp) FindString(s string) (string, bool, error) {
	caps, err := re.search(s)
	if err != nil || caps == nil {
		return "", false, err
	}
	return s[caps[0]:caps[1]], true, nil
}

// FindStringSubmatch returns the matched text followed by the text
// of each capture group for the leftmost match, or nil if there is
// no match. Groups that did not participate yield empty strings; use
// FindStringSubmatchIndex to distinguish them from empty matches.
func (re *Regexp) FindStringSubmatch(s string) ([]string, error) {
	caps, err := re.search(s)
	if err != nil || caps == nil {
		return nil, err
	}
	out := make([]string, len(caps)/2)
	for i := range out {
		lo, hi := caps[2*i], caps[2*i+1]
		if lo >= 0 {
			out[i] = s[lo:hi]
		}
	}
	return out, nil
}

// FindStringSubmatchIndex returns byte-offset pairs for the whole
// match and each capture group, or nil if there is no match. Groups
// that did not participate yield the pair [-1, -1].
func (re *Regexp) FindStringSubmatchIndex(s string) ([]int, error) {
	caps, err := re.search(s)
	if err != nil || caps == nil {
		return nil, err
	}
	out := make([]int, len(caps))
	copy(out, caps)
	return out, nil
}

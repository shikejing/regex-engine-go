package regex

import (
	"strings"
	"testing"
	"time"
)

func TestLinearScaling(t *testing.T) {
	re := MustCompile(`\d{3}-\d{4}`)
	var times []time.Duration
	for _, n := range []int{100_000, 200_000, 400_000} {
		s := strings.Repeat("abcdefgh ", n) // no match, worst case scan
		start := time.Now()
		ok, err := re.MatchString(s)
		if err != nil || ok {
			t.Fatalf("%v %v", ok, err)
		}
		times = append(times, time.Since(start))
	}
	t.Logf("times: %v", times)
	// Allow generous constant factors; quadratic would blow up 4x+ per doubling.
	ratio := float64(times[2]) / float64(times[0])
	if ratio > 8 {
		t.Fatalf("scaling looks super-linear: %v (ratio %.1f for 4x input)", times, ratio)
	}
}

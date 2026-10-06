package regex

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPathologicalPikeLinear 病态回溯形态的模式（嵌套量词）在 Pike VM 下
// 保持线性：即使对诱发指数回溯的输入也应快速完成。
func TestPathologicalPikeLinear(t *testing.T) {
	// 所有模式都要求一个输入中不存在的结尾 'b'，保证不存在匹配。
	patterns := []string{
		`(a+)+b`,
		`(a*)*b`,
		`(a|a)*b`,
		`(a|aa)+b`,
		`(.*)*b`,
	}
	input := strings.Repeat("a", 5000) + "!" // 不存在匹配，最坏情况
	for _, pat := range patterns {
		re := MustCompile(pat)
		start := time.Now()
		ok, err := re.Match(input)
		if err != nil {
			t.Fatalf("%q: %v", pat, err)
		}
		if ok {
			t.Errorf("%q should not match", pat)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%q took %v, want linear-time behavior", pat, d)
		}
	}
}

// TestPathologicalBacktrackBudget 必须回溯的模式（含反向引用）在
// 病态输入下以 ErrBacktrackLimit 可区分地终止，而不是卡死或返回错误结果。
func TestPathologicalBacktrackBudget(t *testing.T) {
	re := MustCompile(`(a+)+\1$`)
	input := strings.Repeat("a", 200) + "!"
	done := make(chan error, 1)
	go func() {
		_, err := re.FindSubmatch(input)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBacktrackLimit) {
			t.Fatalf("got err=%v, want ErrBacktrackLimit", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("backtracker did not terminate within 10s")
	}
}

// TestBacktrackBudgetKeepsCorrectness 预算终止不影响正常含反引模式的正确性。
func TestBacktrackBudgetKeepsCorrectness(t *testing.T) {
	re := MustCompile(`(\w+) \1`)
	if !mustMatch(t, re, "hello hello") {
		t.Fatal("should match")
	}
	if mustMatch(t, re, "hello world") {
		t.Fatal("should not match")
	}
}

// TestLargeInput 远超模式长度的大输入：未加锚点的搜索保持线性，
// 且整体匹配结果正确。
func TestLargeInput(t *testing.T) {
	re := MustCompile(`ERROR: [a-z]+ \((\d+)\)`)
	var sb strings.Builder
	for i := 0; i < 20000; i++ {
		sb.WriteString("INFO: all good\n")
	}
	sb.WriteString("ERROR: disk (42)\n")
	for i := 0; i < 20000; i++ {
		sb.WriteString("INFO: all good\n")
	}
	input := sb.String()
	start := time.Now()
	slots, err := re.FindSubmatch(input)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("search on %d bytes took %v", len(input), d)
	}
	if slots == nil {
		t.Fatal("no match")
	}
	if got := input[slots[0]:slots[1]]; got != "ERROR: disk (42)" {
		t.Fatalf("match = %q", got)
	}
	if got := input[slots[2]:slots[3]]; got != "42" {
		t.Fatalf("group1 = %q", got)
	}
}

// TestConcurrentUse 同一编译产物被多个协程并发使用，
// 结果必须一致（配合 go test -race 检测数据竞争）。
func TestConcurrentUse(t *testing.T) {
	re := MustCompile(`(\w+)@(\w+)\.com`)
	inputs := []string{
		"contact alice@example.com please",
		"bob@test.com",
		"no email here",
		"multi a@x.com and b@y.com",
	}
	want := make([]string, len(inputs))
	for i, in := range inputs {
		loc, _ := re.Find(in)
		if loc != nil {
			want[i] = in[loc[0]:loc[1]]
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for iter := 0; iter < 200; iter++ {
				in := inputs[(g+iter)%len(inputs)]
				loc, err := re.Find(in)
				if err != nil {
					t.Error(err)
					return
				}
				got := ""
				if loc != nil {
					got = in[loc[0]:loc[1]]
				}
				if got != want[(g+iter)%len(inputs)] {
					t.Errorf("input %q: got %q, want %q", in, got, want[(g+iter)%len(inputs)])
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestConcurrentUseBackref 含反引的模式并发复用。
func TestConcurrentUseBackref(t *testing.T) {
	re := MustCompile(`(\w+) \1`)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				ok1, err1 := re.Match("hi hi")
				ok2, err2 := re.Match("hi ho")
				if err1 != nil || err2 != nil || !ok1 || ok2 {
					t.Error("inconsistent result")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestDeterminism 同一模式与输入反复运行结果完全一致。
func TestDeterminism(t *testing.T) {
	re := MustCompile(`(a|ab)(c|bcd)(d*)`)
	input := "abcd"
	first, err := re.FindSubmatch(input)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		got, err := re.FindSubmatch(input)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(first) {
			t.Fatalf("slot count changed: %v vs %v", got, first)
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d: slots %v differ from %v", i, got, first)
			}
		}
	}
}

// TestMatchBudgetErrorDistinguishable Match 在预算超限时返回
// 可区分的错误，而不是静默返回 false。
func TestMatchBudgetErrorDistinguishable(t *testing.T) {
	re := MustCompile(`(a+)+\1$`)
	input := strings.Repeat("a", 200) + "!"
	_, err := re.Match(input)
	if !errors.Is(err, ErrBacktrackLimit) {
		t.Fatalf("got %v, want ErrBacktrackLimit", err)
	}
}

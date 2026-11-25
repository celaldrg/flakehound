package analyze

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/celaldrg/flakehound/internal/model"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// hist builds runs for a single test: spec like "P:a F:a P:b" = status:label.
func hist(spec string) []model.Run {
	var rs []model.Run
	for i, tok := range strings.Fields(spec) {
		st := map[byte]model.Status{'P': model.Pass, 'F': model.Fail, 'R': model.Flaky, 'S': model.Skip}[tok[0]]
		rs = append(rs, model.Run{ID: fmt.Sprint(i), Label: tok[2:], Time: t0.Add(time.Duration(i) * time.Minute),
			Cases: []model.Case{{Suite: "s", Name: "t", Status: st, Message: "boom 123"}}})
	}
	return rs
}

func verdict(t *testing.T, spec string, opt Options) Verdict {
	t.Helper()
	rep := Analyze(hist(spec), opt)
	if len(rep.Tests) != 1 {
		if strings.Contains(spec, "S:") && len(rep.Tests) == 0 {
			return ""
		}
		t.Fatalf("got %d tests", len(rep.Tests))
	}
	return rep.Tests[0].Verdict
}

func TestVerdicts(t *testing.T) {
	cases := []struct {
		name, spec string
		want       Verdict
	}{
		{"all pass", "P:a P:b P:c", Stable},
		{"same commit differs", "P:a F:a P:a", FlakyV},
		{"same commit differs even with 2 runs", "F:a P:a", FlakyV},
		{"retry rescued in a single run", "R:a", FlakyV},
		{"fails every run", "F:a F:b F:c", Broken},
		{"fails every run same commit", "F:a F:a F:a", Broken},
		{"regression: pass then stuck failing", "P:a P:b P:c F:d F:e", Regressed},
		{"fix: failures then long pass streak", "F:a F:b P:c P:d P:e P:f P:g", Recovered},
		{"intermittent across commits", "P:a F:b P:c F:d P:e", FlakyV},
		{"one fresh failure", "P:a P:b P:c F:d", Unknown},
		{"too few runs", "F:a P:b", Unknown},
		{"skips only", "S:a S:b", ""},
	}
	for _, tc := range cases {
		if got := verdict(t, tc.spec, Options{}); got != tc.want {
			t.Errorf("%s (%s): got %q want %q", tc.name, tc.spec, got, tc.want)
		}
	}
}

func TestSkipsDoNotCount(t *testing.T) {
	rep := Analyze(hist("P:a S:b F:a"), Options{})
	if got := rep.Tests[0]; got.Runs != 2 || got.Fails != 1 {
		t.Errorf("runs=%d fails=%d", got.Runs, got.Fails)
	}
}

func TestStats(t *testing.T) {
	rep := Analyze(hist("P:a F:a P:a F:b P:b P:b"), Options{})
	x := rep.Tests[0]
	if x.Runs != 6 || x.Fails != 2 || x.Flips != 4 || x.MixedLabels != 2 || x.Labels != 2 {
		t.Errorf("%+v", x)
	}
	if math.Abs(x.Rate-1.0/3) > 1e-9 || x.Lo >= x.Rate || x.Hi <= x.Rate {
		t.Errorf("rate/CI wrong: %v [%v,%v]", x.Rate, x.Lo, x.Hi)
	}
	if rep.Runs != 6 || rep.Labels != 2 {
		t.Errorf("runs=%d labels=%d", rep.Runs, rep.Labels)
	}
}

func TestWindow(t *testing.T) {
	// old failure falls out of a window of 4
	rep := Analyze(hist("F:a F:b P:c P:d P:e P:f"), Options{Window: 4})
	if rep.Runs != 4 || rep.Tests[0].Verdict != Stable {
		t.Errorf("runs=%d verdict=%s", rep.Runs, rep.Tests[0].Verdict)
	}
}

func TestWilson(t *testing.T) {
	lo, hi := Wilson(0, 0)
	if lo != 0 || hi != 1 {
		t.Errorf("n=0: %v %v", lo, hi)
	}
	lo, hi = Wilson(0, 10)
	if lo != 0 || hi < 0.25 || hi > 0.30 {
		t.Errorf("0/10: %v %v", lo, hi) // known: upper ~0.2775
	}
	lo, hi = Wilson(5, 10)
	if math.Abs(lo-0.2366) > 0.002 || math.Abs(hi-0.7634) > 0.002 {
		t.Errorf("5/10: %v %v", lo, hi)
	}
	lo, hi = Wilson(10, 10)
	if hi < 0.999 || lo < 0.69 {
		t.Errorf("10/10: %v %v", lo, hi)
	}
}

func TestRunsToConfirm(t *testing.T) {
	if got := RunsToConfirm(0.1); got != 29 {
		t.Errorf("got %d", got)
	}
	if RunsToConfirm(0) != 0 || RunsToConfirm(1) != 0 {
		t.Error("edge cases")
	}
}

func TestRedProbability(t *testing.T) {
	// two independent flaky tests failing 50% and 20% => P(green) = 0.5*0.8 = 0.4
	var runs []model.Run
	for i := 0; i < 10; i++ {
		a, b := model.Pass, model.Pass
		if i%2 == 0 {
			a = model.Fail
		}
		if i%5 == 0 {
			b = model.Fail
		}
		runs = append(runs, model.Run{ID: fmt.Sprint(i), Label: "x", Time: t0.Add(time.Duration(i) * time.Second),
			Cases: []model.Case{{Name: "a", Status: a}, {Name: "b", Status: b}, {Name: "c", Status: model.Pass}}})
	}
	rep := Analyze(runs, Options{})
	if math.Abs(rep.RedProb-0.6) > 1e-9 || math.Abs(rep.ExpectedRuns-2.5) > 1e-9 {
		t.Errorf("red=%v expected=%v", rep.RedProb, rep.ExpectedRuns)
	}
	if rep.Counts[FlakyV] != 2 || rep.Counts[Stable] != 1 {
		t.Errorf("%v", rep.Counts)
	}
	if rep.Tests[0].Key != "a" {
		t.Errorf("highest rate should rank first, got %s", rep.Tests[0].Key)
	}
	if rep.FailedRuns != 6 || rep.FlakyFailedRuns != 6 {
		t.Errorf("failed=%d flakyOnly=%d", rep.FailedRuns, rep.FlakyFailedRuns)
	}
}

func TestFlakyOnlyAttribution(t *testing.T) {
	runs := []model.Run{
		{ID: "1", Label: "x", Time: t0, Cases: []model.Case{{Name: "flaky", Status: model.Fail}, {Name: "bug", Status: model.Fail}}},
		{ID: "2", Label: "y", Time: t0.Add(1), Cases: []model.Case{{Name: "flaky", Status: model.Pass}, {Name: "bug", Status: model.Fail}}},
		{ID: "3", Label: "x", Time: t0.Add(2), Cases: []model.Case{{Name: "flaky", Status: model.Pass}}},
		{ID: "4", Label: "w", Time: t0.Add(3), Cases: []model.Case{{Name: "flaky", Status: model.Fail}}},
	}
	rep := Analyze(runs, Options{})
	if rep.FailedRuns != 3 || rep.FlakyFailedRuns != 1 {
		t.Errorf("failed=%d flakyOnly=%d", rep.FailedRuns, rep.FlakyFailedRuns)
	}
}

func TestEmpty(t *testing.T) {
	rep := Analyze(nil, Options{})
	if rep.Runs != 0 || len(rep.Tests) != 0 || rep.RedProb != 0 || rep.ExpectedRuns != 1 {
		t.Errorf("%+v", rep)
	}
}

func TestSignature(t *testing.T) {
	a := Signature("dial tcp 127.0.0.1:41233: connection refused at 2026-11-01T10:00:00Z")
	b := Signature("dial tcp 10.0.0.7:5432: connection refused at 2026-11-02T11:30:12.123Z")
	if a != b {
		t.Errorf("signatures differ:\n%s\n%s", a, b)
	}
	if Signature("got 12 want 13") != Signature("got 99 want 100") {
		t.Error("numbers should collapse")
	}
	if Signature("timeout after 5s") != Signature("timeout after 250ms") {
		t.Error("durations should collapse")
	}
	if Signature("open /tmp/x123/a.txt: no such file") != Signature("open /tmp/zzz/a.txt: no such file") {
		t.Error("tmp paths should collapse")
	}
	if Signature("object 0xc000123 nil") != Signature("object 0xdeadbeef nil") {
		t.Error("hex should collapse")
	}
	if Signature("alpha") == Signature("beta") {
		t.Error("different messages must differ")
	}
	long := Signature(strings.Repeat("x", 500))
	if len(long) > 160 {
		t.Errorf("not truncated: %d", len(long))
	}
	if got := Signature("Error\nexpected 5 got 6\nstack"); !strings.Contains(got, "expected N got N") {
		t.Errorf("short first line should pull in the second: %q", got)
	}
}

func TestKindOf(t *testing.T) {
	cases := map[string]string{
		"WARNING: DATA RACE":                       "data race",
		"context deadline exceeded":                "timeout",
		"Exceeded timeout of 5000 ms":              "timeout",
		"listen tcp :8080: address already in use": "port / address in use",
		"read: connection reset by peer":           "network",
		"HTTP 429 Too Many Requests":               "rate limit",
		"UNIQUE constraint failed: users.email":    "shared state / ordering",
		"too many open files":                      "resources",
		"ENOENT: no such file or directory":        "missing file / async",
		"expected 3 got 4":                         "",
	}
	for msg, want := range cases {
		if got := kindOf(msg); got != want {
			t.Errorf("%q: got %q want %q", msg, got, want)
		}
	}
}

func TestDiagnoseTiming(t *testing.T) {
	tt := Test{Verdict: FlakyV, MedianPass: 100 * time.Millisecond, MedianFail: 450 * time.Millisecond,
		Messages: []Cluster{{Example: "assertion failed", Count: 1, Signature: "assertion failed"}}}
	c := Diagnose(tt)
	if c.Kind != "timing" || !strings.Contains(c.Detail, "x4.5") {
		t.Errorf("%+v", c)
	}
}

func TestDiagnoseFallbacks(t *testing.T) {
	if c := Diagnose(Test{Verdict: FlakyV, Messages: []Cluster{{Example: "a"}, {Example: "b"}}}); c.Kind != "varied failures" {
		t.Errorf("%+v", c)
	}
	if c := Diagnose(Test{Verdict: FlakyV, Messages: []Cluster{{Example: "a"}}}); c.Kind != "nondeterministic" {
		t.Errorf("%+v", c)
	}
	if c := Diagnose(Test{}); c.Kind != "" {
		t.Errorf("%+v", c)
	}
}

func TestClusterAcrossTests(t *testing.T) {
	mk := func(name, msg string, fail bool) model.Case {
		st := model.Pass
		if fail {
			st = model.Fail
		}
		return model.Case{Name: name, Status: st, Message: msg}
	}
	var runs []model.Run
	for i := 0; i < 6; i++ {
		f := i%2 == 0
		runs = append(runs, model.Run{ID: fmt.Sprint(i), Label: "c", Time: t0.Add(time.Duration(i) * time.Second), Cases: []model.Case{
			mk("a", fmt.Sprintf("dial tcp 127.0.0.1:%d: connection refused", 4000+i), f),
			mk("b", fmt.Sprintf("dial tcp 127.0.0.1:%d: connection refused", 5000+i), f && i < 4),
		}})
	}
	rep := Analyze(runs, Options{})
	if len(rep.Clusters) != 1 || rep.Clusters[0].Count != 5 || len(rep.Clusters[0].Tests) != 2 || rep.Clusters[0].Kind != "network" {
		t.Errorf("%+v", rep.Clusters)
	}
}

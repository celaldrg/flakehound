// Package analyze turns run history into flakiness verdicts.
package analyze

import (
	"math"
	"sort"
	"time"

	"github.com/celaldrg/flakehound/internal/model"
)

// Verdict classifies a test.
type Verdict string

const (
	Stable    Verdict = "stable"
	FlakyV    Verdict = "flaky"     // same code, different outcomes (or retry-rescued)
	Broken    Verdict = "broken"    // fails every time
	Regressed Verdict = "regressed" // passed before, now fails consistently: probably a real bug
	Recovered Verdict = "recovered" // used to fail, passes consistently since
	Unknown   Verdict = "unknown"   // not enough runs to say
)

// Options tune the analysis.
type Options struct {
	Window     int // only the newest N runs (0 = all)
	MinRuns    int // runs a test needs before a verdict (default 3)
	RecoverRun int // consecutive passes that count as recovered (default 5)
}

func (o Options) withDefaults() Options {
	if o.MinRuns <= 0 {
		o.MinRuns = 3
	}
	if o.RecoverRun <= 0 {
		o.RecoverRun = 5
	}
	return o
}

// Test is the aggregate record of one test.
type Test struct {
	Key, Suite, Name string
	Runs             int // executions that counted (pass, fail, flaky)
	Fails            int // runs where it failed at least once, including retry-rescued
	Rescued          int // runs that failed then passed on retry
	Flips            int // pass<->fail transitions in time order
	MixedLabels      int // labels (commits) that saw both a pass and a fail
	Labels           int // distinct labels it ran under
	Rate             float64
	Lo, Hi           float64 // Wilson 95% interval of the failure rate
	Verdict          Verdict
	MedianPass       time.Duration
	MedianFail       time.Duration
	Cause            Cause
	Messages         []Cluster
	LastFail         time.Time
}

// Report is the result of analysing a history.
type Report struct {
	Runs, Labels    int
	From, To        time.Time
	Tests           []Test // sorted: flaky by rate desc, then broken, regressed, ...
	Counts          map[Verdict]int
	RedProb         float64 // chance a green commit goes red because of flaky tests
	ExpectedRuns    float64 // expected CI attempts until green
	FlakyFailedRuns int     // runs whose only failures were flaky tests
	FailedRuns      int
	Clusters        []Cluster
}

type obs struct {
	t     time.Time
	label string
	st    model.Status
	dur   time.Duration
	msg   string
}

// Analyze computes verdicts for every test in runs.
func Analyze(runs []model.Run, opt Options) Report {
	opt = opt.withDefaults()
	rs := append([]model.Run(nil), runs...)
	model.SortRuns(rs)
	if opt.Window > 0 && len(rs) > opt.Window {
		rs = rs[len(rs)-opt.Window:]
	}
	rep := Report{Runs: len(rs), Counts: map[Verdict]int{}}
	labels := map[string]bool{}
	byTest := map[string][]obs{}
	meta := map[string][2]string{}
	for _, r := range rs {
		labels[r.Label] = true
		if rep.From.IsZero() || r.Time.Before(rep.From) {
			rep.From = r.Time
		}
		if r.Time.After(rep.To) {
			rep.To = r.Time
		}
		for _, c := range r.Cases {
			if c.Status == model.Skip {
				continue
			}
			k := c.Key()
			byTest[k] = append(byTest[k], obs{r.Time, r.Label, c.Status, c.Duration, c.Message})
			meta[k] = [2]string{c.Suite, c.Name}
		}
	}
	rep.Labels = len(labels)
	for k, os := range byTest {
		t := analyzeTest(k, meta[k][0], meta[k][1], os, opt)
		rep.Tests = append(rep.Tests, t)
		rep.Counts[t.Verdict]++
	}
	sort.Slice(rep.Tests, func(i, j int) bool {
		a, b := rep.Tests[i], rep.Tests[j]
		if ra, rb := rank(a.Verdict), rank(b.Verdict); ra != rb {
			return ra < rb
		}
		if a.Rate != b.Rate {
			return a.Rate > b.Rate
		}
		if a.Runs != b.Runs {
			return a.Runs > b.Runs
		}
		return a.Key < b.Key
	})
	// Chance that a clean commit goes red: 1 - prod(1 - p) over the flaky tests.
	keep := 1.0
	for _, t := range rep.Tests {
		if t.Verdict == FlakyV {
			keep *= 1 - t.Rate
		}
	}
	rep.RedProb = 1 - keep
	rep.ExpectedRuns = 1 / keep
	// How many failed runs were explained entirely by flaky tests.
	flaky := map[string]bool{}
	for _, t := range rep.Tests {
		if t.Verdict == FlakyV {
			flaky[t.Key] = true
		}
	}
	for _, r := range rs {
		failed, onlyFlaky := false, true
		for _, c := range r.Cases {
			if c.Status == model.Fail {
				failed = true
				if !flaky[c.Key()] {
					onlyFlaky = false
				}
			}
		}
		if failed {
			rep.FailedRuns++
			if onlyFlaky {
				rep.FlakyFailedRuns++
			}
		}
	}
	rep.Clusters = clusterAll(rep.Tests)
	return rep
}

func rank(v Verdict) int {
	switch v {
	case FlakyV:
		return 0
	case Regressed:
		return 1
	case Broken:
		return 2
	case Recovered:
		return 3
	case Unknown:
		return 4
	}
	return 5
}

func analyzeTest(key, suite, name string, os []obs, opt Options) Test {
	t := Test{Key: key, Suite: suite, Name: name}
	type lab struct{ pass, fail bool }
	labs := map[string]*lab{}
	var passDur, failDur []time.Duration
	var seq []bool // true = failed
	msgs := map[string]int{}
	for _, o := range os {
		l := labs[o.label]
		if l == nil {
			l = &lab{}
			labs[o.label] = l
		}
		t.Runs++
		switch o.st {
		case model.Pass:
			l.pass = true
			seq = append(seq, false)
			if o.dur > 0 {
				passDur = append(passDur, o.dur)
			}
		case model.Fail:
			l.fail = true
			t.Fails++
			seq = append(seq, true)
			t.LastFail = o.t
			if o.dur > 0 {
				failDur = append(failDur, o.dur)
			}
		case model.Flaky:
			l.pass, l.fail = true, true
			t.Fails++
			t.Rescued++
			seq = append(seq, true)
			t.LastFail = o.t
		}
		if o.msg != "" && o.st != model.Pass {
			msgs[o.msg]++
		}
	}
	t.Labels = len(labs)
	for _, l := range labs {
		if l.pass && l.fail {
			t.MixedLabels++
		}
	}
	for i := 1; i < len(seq); i++ {
		if seq[i] != seq[i-1] {
			t.Flips++
		}
	}
	t.Rate = float64(t.Fails) / float64(t.Runs)
	t.Lo, t.Hi = Wilson(t.Fails, t.Runs)
	t.MedianPass, t.MedianFail = median(passDur), median(failDur)
	t.Verdict = classify(t, seq, opt)
	if t.Fails > 0 {
		t.Messages = clusterMessages(os)
		t.Cause = Diagnose(t)
	}
	return t
}

func classify(t Test, seq []bool, opt Options) Verdict {
	switch {
	case t.Fails == 0:
		return Stable
	case t.Rescued > 0 || t.MixedLabels > 0:
		// Same code, different outcomes: that is the definition of flaky.
		if trailingFails(seq) >= t.Runs && t.Rescued == 0 {
			return Broken
		}
		return FlakyV
	case t.Runs < opt.MinRuns:
		return Unknown
	case t.Fails == t.Runs:
		return Broken
	}
	// Failures only ever seen on different code. A clean pass streak after failures is
	// a recovery; failures stuck at the end after passes look like a real regression.
	tf, tp := trailingFails(seq), trailingPasses(seq)
	switch {
	case tf >= 2 && tf == t.Fails:
		return Regressed
	case tp >= opt.RecoverRun:
		return Recovered
	case t.Flips >= 2:
		return FlakyV // on/off across commits with several flips: intermittent
	case tf >= 1 && tf == t.Fails:
		return Unknown // one fresh failure, too early to call
	}
	return Unknown
}

func trailingFails(seq []bool) int {
	n := 0
	for i := len(seq) - 1; i >= 0 && seq[i]; i-- {
		n++
	}
	return n
}

func trailingPasses(seq []bool) int {
	n := 0
	for i := len(seq) - 1; i >= 0 && !seq[i]; i-- {
		n++
	}
	return n
}

// Wilson returns the 95% Wilson score interval for k failures in n trials.
func Wilson(k, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 1
	}
	const z = 1.959964
	p := float64(k) / float64(n)
	nf := float64(n)
	den := 1 + z*z/nf
	centre := (p + z*z/(2*nf)) / den
	half := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / den
	lo, hi = centre-half, centre+half
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return
}

func median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// RunsToConfirm says how many runs a clean test needs to show it is not flaky
// at the given per-run failure probability with 95% confidence (rule of three style).
func RunsToConfirm(p float64) int {
	if p <= 0 || p >= 1 {
		return 0
	}
	return int(math.Ceil(math.Log(0.05) / math.Log(1-p)))
}

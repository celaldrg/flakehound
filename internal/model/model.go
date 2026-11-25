// Package model holds the data types shared by every flakehound package.
package model

import (
	"sort"
	"time"
)

// Status is the outcome of one test in one run.
type Status string

const (
	Pass  Status = "pass"
	Fail  Status = "fail"
	Skip  Status = "skip"
	Flaky Status = "flaky" // failed, then passed on an automatic retry inside the same run
)

// Case is one test result.
type Case struct {
	Suite    string        `json:"suite,omitempty"`
	Name     string        `json:"name"`
	Status   Status        `json:"status"`
	Duration time.Duration `json:"duration,omitempty"`
	Message  string        `json:"message,omitempty"`
	// Attempts is how many times the test was executed in this run (>=1).
	Attempts int `json:"attempts,omitempty"`
}

// Key identifies a test across runs.
func (c Case) Key() string {
	if c.Suite == "" {
		return c.Name
	}
	return c.Suite + "::" + c.Name
}

// Run is one execution of a test suite: a CI job, a local run, a report file.
type Run struct {
	ID     string    `json:"id"`
	Label  string    `json:"label"` // usually a commit; runs with the same label ran the same code
	Time   time.Time `json:"time"`
	Source string    `json:"source,omitempty"`
	Cases  []Case    `json:"cases"`
}

// Failed reports whether anything in the run failed (retried-then-passed does not count).
func (r Run) Failed() bool {
	for _, c := range r.Cases {
		if c.Status == Fail {
			return true
		}
	}
	return false
}

// SortRuns orders runs oldest first, stable for equal times.
func SortRuns(rs []Run) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Time.Before(rs[j].Time) })
}

// Dedupe merges several results for the same test inside one report (reruns)
// into one Case: fail+pass becomes Flaky, only fails stay Fail.
func Dedupe(in []Case) []Case {
	idx := map[string]int{}
	var out []Case
	for _, c := range in {
		if c.Attempts == 0 {
			c.Attempts = 1
		}
		i, ok := idx[c.Key()]
		if !ok {
			idx[c.Key()] = len(out)
			out = append(out, c)
			continue
		}
		p := &out[i]
		p.Attempts += c.Attempts
		p.Duration += c.Duration
		switch {
		case p.Status == Skip:
			p.Status, p.Message = c.Status, c.Message
		case c.Status == Skip:
		case p.Status == Pass && c.Status == Fail, p.Status == Fail && c.Status == Pass,
			p.Status == Flaky || c.Status == Flaky:
			if p.Message == "" {
				p.Message = c.Message
			}
			p.Status = Flaky
		}
		if p.Status == Fail && c.Status == Fail && c.Message != "" {
			p.Message = c.Message
		}
	}
	return out
}

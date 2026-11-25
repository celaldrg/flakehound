package model

import (
	"testing"
	"time"
)

func TestKey(t *testing.T) {
	if (Case{Name: "a"}).Key() != "a" || (Case{Suite: "s", Name: "a"}).Key() != "s::a" {
		t.Error("key format")
	}
}

func TestRunFailed(t *testing.T) {
	r := Run{Cases: []Case{{Status: Pass}, {Status: Flaky}, {Status: Skip}}}
	if r.Failed() {
		t.Error("retry-rescued and skipped tests do not fail a run")
	}
	r.Cases = append(r.Cases, Case{Status: Fail})
	if !r.Failed() {
		t.Error("expected failed")
	}
}

func TestSortRunsStable(t *testing.T) {
	t0 := time.Now()
	rs := []Run{{ID: "c", Time: t0.Add(2)}, {ID: "a", Time: t0}, {ID: "b", Time: t0}}
	SortRuns(rs)
	if rs[0].ID != "a" || rs[1].ID != "b" || rs[2].ID != "c" {
		t.Errorf("%v", rs)
	}
}

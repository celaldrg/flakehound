package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/celaldrg/flakehound/internal/model"
)

func run(id string, at time.Time) model.Run {
	return model.Run{ID: id, Label: "abc", Time: at, Cases: []model.Case{{Name: "t", Status: model.Pass}}}
}

func TestAppendLoadRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "h.jsonl")
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	n, err := Append(p, run("b", now.Add(time.Hour)), run("a", now))
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	rs, skipped, err := Load(p)
	if err != nil || skipped != 0 || len(rs) != 2 {
		t.Fatalf("%v %d %v", rs, skipped, err)
	}
	if rs[0].ID != "a" {
		t.Errorf("runs must be sorted oldest first, got %s", rs[0].ID)
	}
}

func TestAppendIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	r := run("x", time.Now())
	Append(p, r)
	n, err := Append(p, r, r)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	rs, _, _ := Load(p)
	if len(rs) != 1 {
		t.Fatalf("got %d runs", len(rs))
	}
}

func TestLoadMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	rs, sk, err := Load(filepath.Join(dir, "none"))
	if err != nil || len(rs) != 0 || sk != 0 {
		t.Fatalf("missing file: %v %d %v", rs, sk, err)
	}
	p := filepath.Join(dir, "h.jsonl")
	Append(p, run("ok", time.Now()))
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("{not json\n\n{\"id\":\"\"}\n")
	f.Close()
	rs, sk, err = Load(p)
	if err != nil || len(rs) != 1 || sk != 2 {
		t.Fatalf("corrupt: %d runs, %d skipped, %v", len(rs), sk, err)
	}
}

func TestMakeID(t *testing.T) {
	a := MakeID("l", "s", []byte("x"))
	if a != MakeID("l", "s", []byte("x")) {
		t.Error("not deterministic")
	}
	if a == MakeID("l", "s", []byte("y")) || a == MakeID("m", "s", []byte("x")) || a == MakeID("l", "t", []byte("x")) {
		t.Error("collisions across inputs")
	}
}

func TestAppendUnwritable(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	if _, err := Append(filepath.Join(blocker, "h.jsonl"), run("a", time.Now())); err == nil {
		t.Error("expected error when directory cannot be created")
	}
}

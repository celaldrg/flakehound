package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cli(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var o, e bytes.Buffer
	code := run(context.Background(), args, &o, &e)
	return code, o.String(), e.String()
}

func TestMain(m *testing.M) {
	if os.Getenv("FH_HELPER") == "tap" {
		// emits a failing test on odd runs
		if os.Getenv("FLAKEHOUND_RUN")[0]%2 == 1 {
			os.Stdout.WriteString("TAP version 13\nok 1 - steady\nnot ok 2 - wobbly\n# connection reset by peer\n")
		} else {
			os.Stdout.WriteString("TAP version 13\nok 1 - steady\nok 2 - wobbly\n")
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestUsageAndUnknown(t *testing.T) {
	if code, _, e := cli(t); code != 2 || !strings.Contains(e, "Usage:") {
		t.Errorf("no args: %d %s", code, e)
	}
	if code, _, e := cli(t, "bogus"); code != 2 || !strings.Contains(e, "unknown command") {
		t.Errorf("unknown: %d %s", code, e)
	}
	if code, o, _ := cli(t, "help"); code != 0 || !strings.Contains(o, "flakehound run") {
		t.Errorf("help: %d", code)
	}
	if code, o, _ := cli(t, "version"); code != 0 || !strings.Contains(o, "flakehound") {
		t.Errorf("version: %d %s", code, o)
	}
}

func TestAddReportQuarantineGate(t *testing.T) {
	dir := t.TempDir()
	hist := filepath.Join(dir, "h.jsonl")
	// Three CI runs of the same commit: wobbly fails in one of them.
	mk := func(name, wobbly string) string {
		p := filepath.Join(dir, name)
		body := `<testsuite name="s"><testcase classname="c" name="steady"/><testcase classname="c" name="wobbly">` + wobbly + `</testcase></testsuite>`
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	a := mk("a.xml", "")
	b := mk("b.xml", `<failure message="connection reset by peer"/>`)
	c := mk("c.xml", "")
	code, out, e := cli(t, "add", a, b, c, "-label", "abc123", "-history", hist)
	if code != 0 || !strings.Contains(out, "added 3 run(s)") {
		t.Fatalf("add: %d %s %s", code, out, e)
	}
	// idempotent
	if _, out, _ := cli(t, "add", a, "-label", "abc123", "-history", hist); !strings.Contains(out, "added 0 run(s)") {
		t.Errorf("re-add: %s", out)
	}

	code, out, _ = cli(t, "report", "-history", hist, "-fail-on-flaky")
	if code != 1 || !strings.Contains(out, "c::wobbly") || !strings.Contains(out, "[network]") {
		t.Errorf("report: %d\n%s", code, out)
	}
	code, out, _ = cli(t, "report", "-history", hist, "-format", "json")
	if code != 0 || !strings.Contains(out, `"verdict": "flaky"`) {
		t.Errorf("json: %d %s", code, out)
	}
	if code, out, _ = cli(t, "report", "-history", hist, "-format", "md"); code != 0 || !strings.Contains(out, "## flakehound") {
		t.Errorf("md: %d %s", code, out)
	}
	if code, _, _ = cli(t, "report", "-history", hist, "-format", "yaml"); code != 2 {
		t.Errorf("bad format: %d", code)
	}

	code, out, _ = cli(t, "quarantine", "-history", hist, "-format", "go")
	if code != 0 || strings.TrimSpace(out) != "-skip '^(wobbly)$'" {
		t.Errorf("quarantine go: %d %q", code, out)
	}
	code, qf, _ := cli(t, "quarantine", "-history", hist)
	if code != 0 || !strings.Contains(qf, "c::wobbly # fails 1/3") {
		t.Fatalf("quarantine file: %q", qf)
	}
	qpath := filepath.Join(dir, "q.txt")
	os.WriteFile(qpath, []byte(qf), 0o644)

	// gate: a failing report whose only failure is quarantined passes
	code, out, _ = cli(t, "gate", "-quarantine", qpath, b)
	if code != 0 || !strings.Contains(out, "quarantined  c::wobbly") {
		t.Errorf("gate quarantined: %d %s", code, out)
	}
	// a new failure still blocks
	d := filepath.Join(dir, "d.xml")
	os.WriteFile(d, []byte(`<testsuite name="s"><testcase classname="c" name="wobbly"><failure message="x"/></testcase><testcase classname="c" name="newbug"><failure message="real bug"/></testcase></testsuite>`), 0o644)
	code, out, _ = cli(t, "gate", "-quarantine", qpath, d)
	if code != 1 || !strings.Contains(out, "FAILED       c::newbug") || !strings.Contains(out, "real bug") {
		t.Errorf("gate blocking: %d %s", code, out)
	}
	// without a quarantine file everything blocks
	if code, _, _ = cli(t, "gate", "-quarantine", filepath.Join(dir, "missing"), b); code != 1 {
		t.Errorf("gate without file: %d", code)
	}
}

func TestGateRetriedPassNeverBlocks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.xml")
	os.WriteFile(p, []byte(`<testsuite name="s"><testcase classname="c" name="t"><flakyFailure message="x"/></testcase></testsuite>`), 0o644)
	code, out, _ := cli(t, "gate", "-quarantine", filepath.Join(dir, "none"), p)
	if code != 0 || !strings.Contains(out, "retried-ok   c::t") {
		t.Errorf("%d %s", code, out)
	}
}

func TestRunCommand(t *testing.T) {
	dir := t.TempDir()
	hist := filepath.Join(dir, "h.jsonl")
	t.Setenv("FH_HELPER", "tap")
	code, out, e := cli(t, "run", "-n", "6", "-label", "same", "-history", hist, "--", os.Args[0])
	if code != 0 {
		t.Fatalf("run: %d %s %s", code, out, e)
	}
	if !strings.Contains(out, "run  6/6") || !strings.Contains(out, "wobbly") || !strings.Contains(out, "1 flaky") {
		t.Errorf("run output:\n%s", out)
	}
	if _, err := os.Stat(hist); err != nil {
		t.Errorf("history not written: %v", err)
	}
	// -no-save leaves no file
	hist2 := filepath.Join(dir, "h2.jsonl")
	if code, _, _ := cli(t, "run", "-n", "2", "-no-save", "-label", "z", "-history", hist2, "--", os.Args[0]); code != 0 {
		t.Errorf("no-save: %d", code)
	}
	if _, err := os.Stat(hist2); err == nil {
		t.Error("-no-save wrote history")
	}
}

func TestRunErrors(t *testing.T) {
	if code, _, e := cli(t, "run"); code != 2 || !strings.Contains(e, "no command") {
		t.Errorf("%d %s", code, e)
	}
	if code, _, _ := cli(t, "run", "-n", "1", "-no-save", "--", "/no/such/binary"); code != 2 {
		t.Errorf("missing binary: %d", code)
	}
	if code, _, _ := cli(t, "run", "-bogus"); code != 2 {
		t.Errorf("bad flag: %d", code)
	}
}

func TestAddErrors(t *testing.T) {
	dir := t.TempDir()
	hist := filepath.Join(dir, "h.jsonl")
	if code, _, _ := cli(t, "add", "-history", hist); code != 2 {
		t.Errorf("no files: %d", code)
	}
	if code, _, _ := cli(t, "add", "-history", hist, filepath.Join(dir, "nope.xml")); code != 2 {
		t.Errorf("missing file: %d", code)
	}
	bad := filepath.Join(dir, "bad.txt")
	os.WriteFile(bad, []byte("not a report"), 0o644)
	if code, _, e := cli(t, "add", "-history", hist, bad); code != 2 || !strings.Contains(e, "unrecognised") {
		t.Errorf("bad report: %d %s", code, e)
	}
	ok := filepath.Join(dir, "ok.xml")
	os.WriteFile(ok, []byte(`<testsuite><testcase name="a"/></testsuite>`), 0o644)
	if code, _, _ := cli(t, "add", "-history", hist, "-time", "yesterday", ok); code != 2 {
		t.Errorf("bad -time: %d", code)
	}
	if code, out, _ := cli(t, "add", "-history", hist, "-time", "2026-01-02T03:04:05Z", "-label", "L", ok); code != 0 || !strings.Contains(out, "added 1") {
		t.Errorf("good -time: %d %s", code, out)
	}
}

func TestReportEmptyHistory(t *testing.T) {
	code, out, _ := cli(t, "report", "-history", filepath.Join(t.TempDir(), "none.jsonl"))
	if code != 0 || !strings.Contains(out, "No runs in history") {
		t.Errorf("%d %s", code, out)
	}
}

func TestInterleave(t *testing.T) {
	got := interleave([]string{"a.xml", "-label", "x", "b.xml", "-all", "c.xml", "-format=json"})
	want := []string{"-label", "x", "-all", "-format=json", "a.xml", "b.xml", "c.xml"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v", got)
	}
}

func TestDefaultLabel(t *testing.T) {
	t.Setenv("GITHUB_SHA", "0123456789abcdef")
	if got := defaultLabel(); got != "0123456789" {
		t.Errorf("got %q", got)
	}
}

func TestInterleaveDoubleDash(t *testing.T) {
	got := interleave([]string{"-n", "3", "--", "go", "test", "-json"})
	want := []string{"-n", "3", "go", "test", "-json"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v", got)
	}
}

func TestRunQuietHidesProgress(t *testing.T) {
	t.Setenv("FH_HELPER", "tap")
	_, out, _ := cli(t, "run", "-q", "-n", "2", "-no-save", "-label", "q", "--", os.Args[0])
	if strings.Contains(out, "run  1/2") {
		t.Errorf("progress shown: %s", out)
	}
}

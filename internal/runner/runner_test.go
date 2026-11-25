package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celaldrg/flakehound/internal/parse"
)

// TestMain lets this test binary act as the command under test.
func TestMain(m *testing.M) {
	switch os.Getenv("FH_HELPER") {
	case "tap":
		// fails on every third run, driven by FLAKEHOUND_RUN
		n := os.Getenv("FLAKEHOUND_RUN")
		fmt.Println("TAP version 13")
		fmt.Println("ok 1 - steady")
		if n == "3" {
			fmt.Println("not ok 2 - sometimes")
			os.Exit(1)
		}
		fmt.Println("ok 2 - sometimes")
		os.Exit(0)
	case "junitfile":
		path := os.Getenv("FH_REPORT")
		body := `<testsuite name="s"><testcase classname="c" name="t"/></testsuite>`
		if os.Getenv("FLAKEHOUND_RUN") == "2" {
			body = `<testsuite name="s"><testcase classname="c" name="t"><failure message="x"/></testcase></testsuite>`
		}
		os.WriteFile(path, []byte(body), 0o644)
		os.Exit(0)
	case "garbage":
		fmt.Println("this is not a report")
		os.Exit(0)
	case "sleep":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helper(t *testing.T, mode string, env ...string) []string {
	t.Helper()
	t.Setenv("FH_HELPER", mode)
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		t.Setenv(k, v)
	}
	return []string{os.Args[0]}
}

func TestExecuteStdoutReport(t *testing.T) {
	cmd := helper(t, "tap")
	var out bytes.Buffer
	runs, err := Execute(context.Background(), Config{Command: cmd, Times: 4, Label: "L", Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 4 {
		t.Fatalf("got %d runs", len(runs))
	}
	failed := 0
	ids := map[string]bool{}
	for _, r := range runs {
		if r.Label != "L" {
			t.Errorf("label %q", r.Label)
		}
		ids[r.ID] = true
		if r.Failed() {
			failed++
		}
	}
	if failed != 1 || !runs[2].Failed() {
		t.Errorf("expected only run 3 to fail, failed=%d", failed)
	}
	if len(ids) != 4 {
		t.Errorf("run ids must be unique: %v", ids)
	}
	if !strings.Contains(out.String(), "run  3/4") || !strings.Contains(out.String(), "1 fail") {
		t.Errorf("progress output: %s", out.String())
	}
}

func TestExecuteStopOnFail(t *testing.T) {
	cmd := helper(t, "tap")
	runs, err := Execute(context.Background(), Config{Command: cmd, Times: 10, StopOnFail: true})
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs=%d err=%v", len(runs), err)
	}
}

func TestExecuteReportFile(t *testing.T) {
	dir := t.TempDir()
	cmd := helper(t, "junitfile", "FH_REPORT="+filepath.Join(dir, "out.xml"))
	runs, err := Execute(context.Background(), Config{Command: cmd, Times: 3, Report: "out.xml", Dir: dir, Format: parse.JUnit})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 || !runs[1].Failed() || runs[0].Failed() {
		t.Errorf("unexpected: %+v", runs)
	}
}

func TestExecuteMissingReport(t *testing.T) {
	cmd := helper(t, "tap") // writes no file
	_, err := Execute(context.Background(), Config{Command: cmd, Times: 1, Report: filepath.Join(t.TempDir(), "none*.xml")})
	if err == nil || !strings.Contains(err.Error(), "no report") {
		t.Errorf("err=%v", err)
	}
}

func TestExecuteGarbageOutput(t *testing.T) {
	cmd := helper(t, "garbage")
	runs, err := Execute(context.Background(), Config{Command: cmd, Times: 2})
	if err == nil || len(runs) != 0 || !strings.Contains(err.Error(), "run 1") {
		t.Errorf("runs=%d err=%v", len(runs), err)
	}
}

func TestExecuteBadInput(t *testing.T) {
	if _, err := Execute(context.Background(), Config{Times: 1}); err == nil {
		t.Error("no command")
	}
	if _, err := Execute(context.Background(), Config{Command: []string{"x"}, Times: 0}); err == nil {
		t.Error("n=0")
	}
	if _, err := Execute(context.Background(), Config{Command: []string{"/definitely/not/here"}, Times: 1}); err == nil {
		t.Error("missing binary")
	}
}

func TestExecuteTimeout(t *testing.T) {
	cmd := helper(t, "sleep")
	start := time.Now()
	_, err := Execute(context.Background(), Config{Command: cmd, Times: 1, Timeout: 100 * time.Millisecond})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Errorf("err=%v after %v", err, time.Since(start))
	}
}

func TestExecuteCancelled(t *testing.T) {
	cmd := helper(t, "tap")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runs, err := Execute(ctx, Config{Command: cmd, Times: 3})
	if err == nil || len(runs) != 0 {
		t.Errorf("runs=%d err=%v", len(runs), err)
	}
}

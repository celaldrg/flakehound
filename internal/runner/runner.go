// Package runner executes a test command repeatedly and collects one Run per execution.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/celaldrg/flakehound/internal/model"
	"github.com/celaldrg/flakehound/internal/parse"
	"github.com/celaldrg/flakehound/internal/store"
)

// Config describes a repeated execution.
type Config struct {
	Command []string
	Times   int
	Format  parse.Format
	// Report is a file or glob the command writes (JUnit/Jest). Empty means the
	// report is the command's stdout (go test -json, TAP, jest --json).
	Report string
	Label  string
	// StopOnFail stops after the first run with a failing test (to catch a flake fast).
	StopOnFail bool
	Timeout    time.Duration // per run, 0 = none
	Stdout     io.Writer     // where progress goes
	Dir        string
	Now        func() time.Time
}

// Execute runs the command cfg.Times times and returns the parsed runs.
// A command that exits non-zero is normal (tests failed); a command that produces
// no parsable report is an error for that run and aborts the series.
func Execute(ctx context.Context, cfg Config) ([]model.Run, error) {
	if len(cfg.Command) == 0 {
		return nil, fmt.Errorf("no command given")
	}
	if cfg.Times < 1 {
		return nil, fmt.Errorf("-n must be at least 1")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	var runs []model.Run
	for i := 1; i <= cfg.Times; i++ {
		if ctx.Err() != nil {
			return runs, ctx.Err()
		}
		start := now()
		data, runErr, err := one(ctx, cfg, i)
		if err != nil {
			return runs, fmt.Errorf("run %d: %w", i, err)
		}
		cases, perr := parse.Parse(data, cfg.Format)
		if perr != nil {
			return runs, fmt.Errorf("run %d: %w (command: %v)", i, perr, runErr)
		}
		r := model.Run{
			ID:     store.MakeID(cfg.Label, "run-"+strconv.FormatInt(start.UnixNano(), 10)+"-"+strconv.Itoa(i), data),
			Label:  cfg.Label,
			Time:   start,
			Source: "run " + strconv.Itoa(i) + "/" + strconv.Itoa(cfg.Times),
			Cases:  cases,
		}
		runs = append(runs, r)
		if cfg.Stdout != nil {
			pass, fail, flaky := tally(cases)
			fmt.Fprintf(cfg.Stdout, "run %2d/%d  %4d pass  %3d fail  %3d retried\n", i, cfg.Times, pass, fail, flaky)
		}
		if cfg.StopOnFail && r.Failed() {
			break
		}
	}
	return runs, nil
}

func tally(cs []model.Case) (pass, fail, flaky int) {
	for _, c := range cs {
		switch c.Status {
		case model.Pass:
			pass++
		case model.Fail:
			fail++
		case model.Flaky:
			flaky++
		}
	}
	return
}

func one(ctx context.Context, cfg Config, i int) (data []byte, runErr error, err error) {
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	if cfg.Report != "" {
		// stale reports from an earlier run would be counted twice
		for _, f := range expand(cfg.Dir, cfg.Report) {
			os.Remove(f)
		}
	}
	cmd := exec.CommandContext(ctx, cfg.Command[0], cfg.Command[1:]...)
	cmd.Dir = cfg.Dir
	cmd.Env = append(os.Environ(), "FLAKEHOUND_RUN="+strconv.Itoa(i), "FLAKEHOUND=1")
	var out bytes.Buffer
	cmd.Stdout = &out
	var errb bytes.Buffer
	cmd.Stderr = &errb
	runErr = cmd.Run()
	if _, notFound := runErr.(*exec.Error); notFound {
		return nil, runErr, runErr
	}
	if cfg.Report == "" {
		return out.Bytes(), runErr, nil
	}
	files := expand(cfg.Dir, cfg.Report)
	if len(files) == 0 {
		return nil, runErr, fmt.Errorf("command wrote no report matching %q", cfg.Report)
	}
	return concat(files)
}

func expand(dir, pattern string) []string {
	if !filepath.IsAbs(pattern) && dir != "" {
		pattern = filepath.Join(dir, pattern)
	}
	m, _ := filepath.Glob(pattern)
	sort.Strings(m)
	return m
}

// concat joins several report files. JUnit files cannot be concatenated byte-wise
// (multiple XML roots are fine for the streaming parser); JSON streams can.
func concat(files []string) ([]byte, error, error) {
	var all []byte
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		all = append(all, b...)
		all = append(all, '\n')
	}
	return all, nil, nil
}

// flakehound finds flaky tests: it repeats your test command, ingests CI reports,
// keeps history, and tells you which tests fail on the same code, how often, and why.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/celaldrg/flakehound/internal/analyze"
	"github.com/celaldrg/flakehound/internal/model"
	"github.com/celaldrg/flakehound/internal/parse"
	"github.com/celaldrg/flakehound/internal/quarantine"
	"github.com/celaldrg/flakehound/internal/render"
	"github.com/celaldrg/flakehound/internal/runner"
	"github.com/celaldrg/flakehound/internal/store"
)

var version = "dev"

const usage = `flakehound: find flaky tests, prove it, and stop them blocking your CI.

Usage:
  flakehound run [-n 10] [-label X] [-- command args...]   repeat a test command, record every run
  flakehound add [-label X] report...                      ingest reports (JUnit, go test -json, TAP, Jest)
  flakehound report [-format text|md|json]                 flaky / broken / regressed tests from history
  flakehound quarantine [-format list|go|pytest|jest]      list flaky tests to skip
  flakehound gate -quarantine FILE report...               exit 1 only for failures that are not quarantined
  flakehound version

History lives in .flakehound/history.jsonl (override with -history).
Exit codes: 0 ok, 1 findings (report -fail-on-flaky, gate), 2 usage or input error.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "run":
		return cmdRun(ctx, rest, stdout, stderr)
	case "add":
		return cmdAdd(rest, stdout, stderr)
	case "report":
		return cmdReport(rest, stdout, stderr)
	case "quarantine":
		return cmdQuarantine(rest, stdout, stderr)
	case "gate":
		return cmdGate(rest, stdout, stderr)
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, "flakehound", version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "flakehound: unknown command %q\n\n%s", cmd, usage)
	return 2
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("flakehound "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func fail(stderr io.Writer, format string, a ...any) int {
	fmt.Fprintf(stderr, "flakehound: "+format+"\n", a...)
	return 2
}

func useColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" || os.Getenv("CLICOLOR_FORCE") != "" {
		return true
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// defaultLabel names the code under test: $GITHUB_SHA, git HEAD, or "local".
func defaultLabel() string {
	if s := os.Getenv("GITHUB_SHA"); s != "" {
		return short(s)
	}
	if out, err := exec.Command("git", "rev-parse", "--short=10", "HEAD").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}
	return "local"
}

func short(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

func cmdRun(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("run", stderr)
	n := fs.Int("n", 10, "how many times to run the command")
	format := fs.String("format", "auto", "report format: auto, junit, gotest, tap, jest")
	reportFile := fs.String("report", "", "file or glob the command writes (default: read stdout)")
	label := fs.String("label", "", "label for the code under test (default: git HEAD)")
	hist := fs.String("history", store.DefaultPath, "history file")
	stopFail := fs.Bool("stop-on-fail", false, "stop at the first failing run")
	timeout := fs.Duration("timeout", 0, "timeout per run")
	noSave := fs.Bool("no-save", false, "do not write history")
	quiet := fs.Bool("q", false, "no per-run progress lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	command := fs.Args()
	if len(command) == 0 {
		return fail(stderr, "no command: flakehound run -n 10 -- go test -json ./...")
	}
	if *label == "" {
		*label = defaultLabel()
	}
	var progress io.Writer = stdout
	if *quiet {
		progress = nil
	}
	runs, err := runner.Execute(ctx, runner.Config{Command: command, Times: *n, Format: parse.Format(*format),
		Report: *reportFile, Label: *label, StopOnFail: *stopFail, Timeout: *timeout, Stdout: progress})
	if len(runs) > 0 && !*noSave {
		if _, serr := store.Append(*hist, runs...); serr != nil {
			return fail(stderr, "%v", serr)
		}
	}
	if err != nil {
		return fail(stderr, "%v", err)
	}
	all, _, _ := store.Load(*hist)
	if *noSave {
		all = runs
	}
	fmt.Fprintln(stdout)
	rep := analyze.Analyze(filterLabel(all, *label), analyze.Options{})
	render.Text(stdout, rep, render.Options{Style: render.Style{Color: useColor(stdout)}})
	return 0
}

func filterLabel(rs []model.Run, label string) []model.Run {
	var out []model.Run
	for _, r := range rs {
		if r.Label == label {
			out = append(out, r)
		}
	}
	return out
}

func cmdAdd(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("add", stderr)
	format := fs.String("format", "auto", "report format")
	label := fs.String("label", "", "commit/label for these reports (default: $GITHUB_SHA, git HEAD)")
	hist := fs.String("history", store.DefaultPath, "history file")
	when := fs.String("time", "", "RFC 3339 time of the run (default: file modification time)")
	if err := fs.Parse(interleave(args)); err != nil {
		return 2
	}
	files := fs.Args()
	if len(files) == 0 {
		return fail(stderr, "no report files given")
	}
	lb := *label
	if lb == "" {
		lb = defaultLabel()
	}
	var runs []model.Run
	for _, f := range files {
		matches, _ := filepath.Glob(f)
		if len(matches) == 0 {
			matches = []string{f}
		}
		for _, m := range matches {
			data, err := os.ReadFile(m)
			if err != nil {
				return fail(stderr, "%v", err)
			}
			cases, err := parse.Parse(data, parse.Format(*format))
			if err != nil {
				return fail(stderr, "%s: %v", m, err)
			}
			t := time.Now()
			if st, err := os.Stat(m); err == nil {
				t = st.ModTime()
			}
			if *when != "" {
				pt, err := time.Parse(time.RFC3339, *when)
				if err != nil {
					return fail(stderr, "-time: %v", err)
				}
				t = pt
			}
			runs = append(runs, model.Run{ID: store.MakeID(lb, filepath.Base(m), data), Label: lb, Time: t, Source: filepath.Base(m), Cases: cases})
		}
	}
	added, err := store.Append(*hist, runs...)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	fmt.Fprintf(stdout, "added %d run(s) to %s (%d already present)\n", added, *hist, len(runs)-added)
	return 0
}

// interleave lets flags appear after positional arguments ("add report.xml -label x").
func interleave(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !isBool(a) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			pos = append(pos, a)
		}
	}
	return append(flags, pos...)
}

func isBool(a string) bool {
	switch strings.TrimLeft(a, "-") {
	case "all", "fail-on-flaky", "stop-on-fail", "no-save", "q":
		return true
	}
	return false
}

func loadHistory(path string, stderr io.Writer) ([]model.Run, error) {
	runs, skipped, err := store.Load(path)
	if err != nil {
		return nil, err
	}
	if skipped > 0 {
		fmt.Fprintf(stderr, "flakehound: skipped %d unreadable line(s) in %s\n", skipped, path)
	}
	return runs, nil
}

func cmdReport(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("report", stderr)
	format := fs.String("format", "text", "text, md or json")
	hist := fs.String("history", store.DefaultPath, "history file")
	window := fs.Int("window", 0, "only the newest N runs (0 = all)")
	minRuns := fs.Int("min-runs", 3, "runs needed before a test gets a verdict")
	all := fs.Bool("all", false, "include stable tests / recovered / unknown")
	limit := fs.Int("limit", 25, "rows per section")
	failOn := fs.Bool("fail-on-flaky", false, "exit 1 if any flaky test is found")
	if err := fs.Parse(interleave(args)); err != nil {
		return 2
	}
	runs, err := loadHistory(*hist, stderr)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	rep := analyze.Analyze(runs, analyze.Options{Window: *window, MinRuns: *minRuns})
	switch *format {
	case "text":
		render.Text(stdout, rep, render.Options{All: *all, Limit: *limit, Style: render.Style{Color: useColor(stdout)}})
	case "md", "markdown":
		render.Markdown(stdout, rep, render.Options{Limit: *limit})
	case "json":
		if err := render.JSON(stdout, rep, *all); err != nil {
			return fail(stderr, "%v", err)
		}
	default:
		return fail(stderr, "unknown -format %q", *format)
	}
	if *failOn && rep.Counts[analyze.FlakyV] > 0 {
		return 1
	}
	return 0
}

func cmdQuarantine(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("quarantine", stderr)
	format := fs.String("format", "file", "file (annotated list), list, go, pytest, jest")
	hist := fs.String("history", store.DefaultPath, "history file")
	minRate := fs.Float64("min-rate", 0.02, "only tests failing at least this often (0-1)")
	if err := fs.Parse(interleave(args)); err != nil {
		return 2
	}
	runs, err := loadHistory(*hist, stderr)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	rep := analyze.Analyze(runs, analyze.Options{})
	sel := quarantine.Select(rep, *minRate)
	if *format == "file" {
		quarantine.Write(stdout, sel)
		return 0
	}
	s, err := quarantine.Render(*format, sel)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if s != "" {
		fmt.Fprintln(stdout, s)
	}
	return 0
}

func cmdGate(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("gate", stderr)
	qfile := fs.String("quarantine", ".flakehound/quarantine.txt", "quarantine file")
	format := fs.String("format", "auto", "report format")
	if err := fs.Parse(interleave(args)); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		return fail(stderr, "no report files given")
	}
	var q quarantine.Set
	if f, err := os.Open(*qfile); err == nil {
		q, err = quarantine.Read(f)
		f.Close()
		if err != nil {
			return fail(stderr, "%v", err)
		}
	} else if !os.IsNotExist(err) {
		return fail(stderr, "%v", err)
	} else {
		q = quarantine.Set{}
	}
	var blocking, ignored, rescued []model.Case
	total := 0
	for _, pattern := range fs.Args() {
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			matches = []string{pattern}
		}
		for _, m := range matches {
			data, err := os.ReadFile(m)
			if err != nil {
				return fail(stderr, "%v", err)
			}
			cases, err := parse.Parse(data, parse.Format(*format))
			if err != nil {
				return fail(stderr, "%s: %v", m, err)
			}
			total += len(cases)
			for _, c := range cases {
				switch {
				case c.Status == model.Flaky:
					rescued = append(rescued, c)
				case c.Status == model.Fail && q[c.Key()]:
					ignored = append(ignored, c)
				case c.Status == model.Fail:
					blocking = append(blocking, c)
				}
			}
		}
	}
	for _, c := range ignored {
		fmt.Fprintf(stdout, "quarantined  %s\n", c.Key())
	}
	for _, c := range rescued {
		fmt.Fprintf(stdout, "retried-ok   %s (failed %d time(s) before passing)\n", c.Key(), c.Attempts-1)
	}
	for _, c := range blocking {
		fmt.Fprintf(stdout, "FAILED       %s\n", c.Key())
		if c.Message != "" {
			fmt.Fprintf(stdout, "             %s\n", strings.ReplaceAll(firstLines(c.Message, 3), "\n", "\n             "))
		}
	}
	fmt.Fprintf(stdout, "\n%d tests · %d failing · %d quarantined failures ignored · %d passed on retry\n", total, len(blocking), len(ignored), len(rescued))
	if len(blocking) > 0 {
		return 1
	}
	return 0
}

func firstLines(s string, n int) string {
	l := strings.Split(s, "\n")
	if len(l) > n {
		l = append(l[:n], "…")
	}
	return strings.Join(l, "\n")
}

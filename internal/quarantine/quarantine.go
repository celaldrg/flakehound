// Package quarantine reads and writes quarantine lists and renders them as
// skip flags for common test runners.
package quarantine

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/celaldrg/flakehound/internal/analyze"
)

// Set is a set of test keys.
type Set map[string]bool

// Read parses a quarantine file: one test key per line, '#' starts a comment.
func Read(r io.Reader) (Set, error) {
	s := Set{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if i := strings.Index(l, " #"); i >= 0 {
			l = strings.TrimSpace(l[:i])
		}
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		s[l] = true
	}
	return s, sc.Err()
}

// Select picks the tests worth quarantining: flaky ones at or above minRate.
func Select(rep analyze.Report, minRate float64) []analyze.Test {
	var out []analyze.Test
	for _, t := range rep.Tests {
		if t.Verdict == analyze.FlakyV && t.Rate >= minRate {
			out = append(out, t)
		}
	}
	return out
}

// Write renders the quarantine file with a comment explaining each entry.
func Write(w io.Writer, ts []analyze.Test) {
	fmt.Fprintln(w, "# flakehound quarantine list: one test per line, failures of these do not fail `flakehound gate`.")
	fmt.Fprintln(w, "# Every entry should have an owner and a fix date. Delete a line when the test is fixed.")
	for _, t := range ts {
		fmt.Fprintf(w, "%s # fails %d/%d (%.0f%%)\n", t.Key, t.Fails, t.Runs, t.Rate*100)
	}
}

// Render emits skip arguments for a test runner.
//
//	list   : one key per line
//	go     : -skip '^(TestA|TestB)$' (Go 1.20+; subtests handled with '/')
//	pytest : --deselect args
//	jest   : --testNamePattern negative lookahead
func Render(format string, ts []analyze.Test) (string, error) {
	keys := make([]string, 0, len(ts))
	for _, t := range ts {
		keys = append(keys, t.Key)
	}
	sort.Strings(keys)
	switch format {
	case "list", "":
		return strings.Join(keys, "\n"), nil
	case "go":
		var names []string
		for _, t := range ts {
			if t.Name == "(package)" {
				continue
			}
			names = append(names, goPattern(t.Name))
		}
		sort.Strings(names)
		if len(names) == 0 {
			return "", nil
		}
		return "-skip '^(" + strings.Join(names, "|") + ")$'", nil
	case "pytest":
		var args []string
		for _, t := range ts {
			id := t.Name
			if t.Suite != "" && strings.Contains(t.Suite, ".") && !strings.Contains(t.Suite, "/") {
				// JUnit classname "tests.test_x.TestY" -> tests/test_x.py::TestY::name
				parts := strings.Split(t.Suite, ".")
				i := 0
				for i < len(parts) && !(len(parts[i]) > 0 && parts[i][0] >= 'A' && parts[i][0] <= 'Z') {
					i++
				}
				file := strings.Join(parts[:i], "/") + ".py"
				id = file
				for _, p := range parts[i:] {
					id += "::" + p
				}
				id += "::" + t.Name
			} else if t.Suite != "" {
				id = t.Suite + "::" + t.Name
			}
			args = append(args, "--deselect "+shellQuote(id))
		}
		sort.Strings(args)
		return strings.Join(args, " "), nil
	case "jest":
		var names []string
		for _, t := range ts {
			names = append(names, regexp.QuoteMeta(t.Name))
		}
		sort.Strings(names)
		if len(names) == 0 {
			return "", nil
		}
		return "--testNamePattern " + shellQuote("^(?!.*(?:"+strings.Join(names, "|")+")$)"), nil
	}
	return "", fmt.Errorf("unknown quarantine format %q (list, go, pytest, jest)", format)
}

// goPattern escapes a Go test name for -skip, which splits on '/' per subtest level.
func goPattern(name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return strings.Join(parts, "/")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

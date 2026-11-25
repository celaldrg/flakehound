// Package render formats analysis results as text, Markdown or JSON.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/celaldrg/flakehound/internal/analyze"
)

// Style controls colour output.
type Style struct{ Color bool }

func (s Style) c(code, text string) string {
	if !s.Color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) bold(t string) string   { return s.c("1", t) }
func (s Style) dim(t string) string    { return s.c("2", t) }
func (s Style) red(t string) string    { return s.c("31", t) }
func (s Style) yellow(t string) string { return s.c("33", t) }
func (s Style) green(t string) string  { return s.c("32", t) }
func (s Style) cyan(t string) string   { return s.c("36", t) }

// Options select what the report shows.
type Options struct {
	All   bool // also list stable tests
	Limit int  // max rows per section (0 = 25)
	Style Style
}

func pct(f float64) string {
	if f >= 0.995 {
		return "100%"
	}
	if f < 0.01 && f > 0 {
		return "<1%"
	}
	return fmt.Sprintf("%.0f%%", f*100)
}

func pad(s string, n int) string {
	w := utf8.RuneCountInString(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

func trunc(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func dur(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// Text writes the human report.
func Text(w io.Writer, rep analyze.Report, o Options) {
	st := o.Style
	limit := o.Limit
	if limit <= 0 {
		limit = 25
	}
	span := ""
	if !rep.From.IsZero() {
		span = " · " + rep.From.Format("2006-01-02") + " → " + rep.To.Format("2006-01-02")
	}
	commits := "commits"
	if rep.Labels == 1 {
		commits = "commit"
	}
	fmt.Fprintf(w, "%s  %d runs · %d %s · %d tests%s\n\n", st.bold("flakehound"), rep.Runs, rep.Labels, commits, len(rep.Tests), span)
	if rep.Runs == 0 {
		fmt.Fprintln(w, "No runs in history yet. Try: flakehound run -n 10 -- go test -json ./...")
		return
	}
	c := rep.Counts
	fmt.Fprintf(w, "%s  %s · %s · %s · %s\n", st.bold("Verdicts"),
		st.yellow(fmt.Sprintf("%d flaky", c[analyze.FlakyV])),
		st.red(fmt.Sprintf("%d broken", c[analyze.Broken])),
		st.red(fmt.Sprintf("%d regressed", c[analyze.Regressed])),
		st.green(fmt.Sprintf("%d stable", c[analyze.Stable])))
	if c[analyze.FlakyV] > 0 {
		fmt.Fprintf(w, "         a clean commit has a %s chance of a red build from flakes alone (%.1f CI runs to get green)\n",
			st.bold(st.yellow(fmt.Sprintf("%.0f%%", rep.RedProb*100))), rep.ExpectedRuns)
	}
	if rep.FlakyFailedRuns > 0 {
		fmt.Fprintf(w, "         %d of %d failed runs were caused only by flaky tests\n", rep.FlakyFailedRuns, rep.FailedRuns)
	}
	fmt.Fprintln(w)
	section(w, st, rep, o, limit, analyze.FlakyV, "Flaky: same code, different results")
	section(w, st, rep, o, limit, analyze.Regressed, "Regressed: started failing and stayed failing (likely a real bug)")
	section(w, st, rep, o, limit, analyze.Broken, "Broken: fails every time")
	if len(rep.Clusters) > 0 {
		fmt.Fprintln(w, st.bold("Failure clusters (flaky tests only)"))
		for i, cl := range rep.Clusters {
			if i >= 5 {
				break
			}
			kind := ""
			if cl.Kind != "" {
				kind = st.cyan(" [" + cl.Kind + "]")
			}
			fmt.Fprintf(w, "  %3dx  %s%s\n", cl.Count, trunc(cl.Signature, 90), kind)
			fmt.Fprintf(w, "        %s\n", st.dim(fmt.Sprintf("in %d test(s)", len(cl.Tests))))
		}
		fmt.Fprintln(w)
	}
	if o.All {
		section(w, st, rep, o, limit, analyze.Recovered, "Recovered")
		section(w, st, rep, o, limit, analyze.Unknown, "Not enough data")
	}
	if c[analyze.FlakyV] > 0 {
		fmt.Fprintf(w, "%s flakehound quarantine > quarantine.txt, then in CI: flakehound gate -quarantine quarantine.txt report\n", st.dim("next:"))
	}
}

func section(w io.Writer, st Style, rep analyze.Report, o Options, limit int, v analyze.Verdict, title string) {
	var rows []analyze.Test
	for _, t := range rep.Tests {
		if t.Verdict == v {
			rows = append(rows, t)
		}
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "%s %s\n", st.bold(title), st.dim(fmt.Sprintf("(%d)", len(rows))))
	fmt.Fprintf(w, "  %s\n", st.dim(pad("RATE", 5)+" "+pad("FAILS", 7)+" "+pad("95% CI", 9)+" "+pad("SAME-COMMIT", 11)+" "+pad("TEST", 46)+" LIKELY CAUSE"))
	for i, t := range rows {
		if i >= limit {
			fmt.Fprintf(w, "  %s\n", st.dim(fmt.Sprintf("… and %d more (use -limit)", len(rows)-i)))
			break
		}
		ci := pct(t.Lo) + "–" + pct(t.Hi)
		same := "-"
		if t.MixedLabels > 0 {
			same = fmt.Sprintf("%d commit", t.MixedLabels)
			if t.MixedLabels != 1 {
				same += "s"
			}
		} else if t.Rescued > 0 {
			same = "retried"
		}
		cause := ""
		if t.Cause.Kind != "" {
			cause = t.Cause.Kind
			if strings.HasPrefix(t.Cause.Detail, "slower") {
				cause += " (" + t.Cause.Detail + ")"
			}
		}
		rate := pad(pct(t.Rate), 5)
		switch {
		case v == analyze.FlakyV:
			rate = st.yellow(rate)
		case v == analyze.Regressed || v == analyze.Broken:
			rate = st.red(rate)
		}
		fmt.Fprintf(w, "  %s %s %s %s %s %s\n", rate,
			pad(fmt.Sprintf("%d/%d", t.Fails, t.Runs), 7), pad(ci, 9), pad(same, 11),
			pad(trunc(t.Key, 46), 46), st.cyan(cause))
	}
	fmt.Fprintln(w)
}

// Markdown writes a report suited to $GITHUB_STEP_SUMMARY or a PR comment.
func Markdown(w io.Writer, rep analyze.Report, o Options) {
	limit := o.Limit
	if limit <= 0 {
		limit = 25
	}
	fmt.Fprintf(w, "## flakehound: %d runs, %d commits, %d tests\n\n", rep.Runs, rep.Labels, len(rep.Tests))
	c := rep.Counts
	fmt.Fprintf(w, "**%d flaky** · %d broken · %d regressed · %d stable\n\n", c[analyze.FlakyV], c[analyze.Broken], c[analyze.Regressed], c[analyze.Stable])
	if c[analyze.FlakyV] > 0 {
		fmt.Fprintf(w, "A clean commit has a **%.0f%%** chance of a red build from flaky tests alone (%.1f CI runs to get green).\n\n", rep.RedProb*100, rep.ExpectedRuns)
		fmt.Fprintln(w, "| Rate | Fails | 95% CI | Test | Likely cause |")
		fmt.Fprintln(w, "|---:|---:|:---:|---|---|")
		n := 0
		for _, t := range rep.Tests {
			if t.Verdict != analyze.FlakyV {
				continue
			}
			if n++; n > limit {
				break
			}
			fmt.Fprintf(w, "| %s | %d/%d | %s–%s | `%s` | %s |\n", pct(t.Rate), t.Fails, t.Runs, pct(t.Lo), pct(t.Hi), strings.ReplaceAll(t.Key, "|", `\|`), t.Cause.Kind)
		}
		fmt.Fprintln(w)
	}
	for _, v := range []analyze.Verdict{analyze.Regressed, analyze.Broken} {
		var names []string
		for _, t := range rep.Tests {
			if t.Verdict == v {
				names = append(names, "`"+t.Key+"`")
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(w, "**%s:** %s\n\n", strings.ToUpper(string(v)[:1])+string(v)[1:], strings.Join(names, ", "))
		}
	}
}

type jsonTest struct {
	Key         string  `json:"key"`
	Verdict     string  `json:"verdict"`
	Runs        int     `json:"runs"`
	Fails       int     `json:"fails"`
	Rescued     int     `json:"rescued"`
	Rate        float64 `json:"rate"`
	CILow       float64 `json:"ci_low"`
	CIHigh      float64 `json:"ci_high"`
	Flips       int     `json:"flips"`
	MixedLabels int     `json:"same_commit_mixed"`
	Cause       string  `json:"cause,omitempty"`
	TopFailure  string  `json:"top_failure,omitempty"`
}

// JSON writes the machine-readable report.
func JSON(w io.Writer, rep analyze.Report, all bool) error {
	out := struct {
		Runs         int            `json:"runs"`
		Commits      int            `json:"commits"`
		Counts       map[string]int `json:"counts"`
		RedProb      float64        `json:"red_probability_from_flakes"`
		ExpectedRuns float64        `json:"expected_runs_to_green"`
		Tests        []jsonTest     `json:"tests"`
	}{Runs: rep.Runs, Commits: rep.Labels, Counts: map[string]int{}, RedProb: rep.RedProb, ExpectedRuns: rep.ExpectedRuns, Tests: []jsonTest{}}
	for v, n := range rep.Counts {
		out.Counts[string(v)] = n
	}
	for _, t := range rep.Tests {
		if !all && (t.Verdict == analyze.Stable) {
			continue
		}
		jt := jsonTest{Key: t.Key, Verdict: string(t.Verdict), Runs: t.Runs, Fails: t.Fails, Rescued: t.Rescued,
			Rate: t.Rate, CILow: t.Lo, CIHigh: t.Hi, Flips: t.Flips, MixedLabels: t.MixedLabels, Cause: t.Cause.Kind}
		if len(t.Messages) > 0 {
			jt.TopFailure = t.Messages[0].Signature
		}
		out.Tests = append(out.Tests, jt)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

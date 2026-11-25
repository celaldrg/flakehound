package analyze

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// Cause is a best-effort guess at why a test is flaky.
type Cause struct {
	Kind   string // short label, e.g. "timeout"
	Detail string // one line of evidence
}

var (
	reHex   = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	reUUID  = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reLong  = regexp.MustCompile(`\b[0-9a-fA-F]{12,}\b`)
	reAddr  = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`)
	rePort  = regexp.MustCompile(`(?:localhost|\[::1?\]|0\.0\.0\.0):\d+`)
	reTime  = regexp.MustCompile(`\d{4}-\d\d-\d\d[T ]\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:?\d\d)?`)
	reDur   = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m|h)\b`)
	reTmp   = regexp.MustCompile(`(?:/tmp|/var/folders|/private/var)/[^\s:'"]+`)
	reNum   = regexp.MustCompile(`\d+`)
	reSpace = regexp.MustCompile(`\s+`)
)

// Signature collapses the volatile parts of a failure message (numbers, addresses,
// ids, timestamps, temp paths) so equal failures cluster together.
func Signature(msg string) string {
	s := strings.TrimSpace(msg)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		// first informative line, but keep a second one for context when the first is generic
		first := strings.TrimSpace(s[:i])
		if len(first) < 24 {
			rest := strings.TrimSpace(s[i+1:])
			if j := strings.IndexByte(rest, '\n'); j >= 0 {
				rest = rest[:j]
			}
			first += " " + strings.TrimSpace(rest)
		}
		s = first
	}
	s = reTime.ReplaceAllString(s, "<time>")
	s = reUUID.ReplaceAllString(s, "<id>")
	s = reHex.ReplaceAllString(s, "<hex>")
	s = reLong.ReplaceAllString(s, "<id>")
	s = reTmp.ReplaceAllString(s, "<tmp>")
	s = rePort.ReplaceAllString(s, "<local>")
	s = reAddr.ReplaceAllString(s, "<addr>")
	s = reDur.ReplaceAllString(s, "<dur>")
	s = reNum.ReplaceAllString(s, "N")
	s = reSpace.ReplaceAllString(s, " ")
	if len(s) > 140 {
		s = s[:140] + "…"
	}
	return s
}

// Cluster is a group of equal failures.
type Cluster struct {
	Signature string
	Count     int
	Tests     []string
	Example   string
	Kind      string
}

func clusterMessages(os []obs) []Cluster {
	idx := map[string]*Cluster{}
	for _, o := range os {
		if o.msg == "" || o.st == "pass" {
			continue
		}
		sig := Signature(o.msg)
		c := idx[sig]
		if c == nil {
			c = &Cluster{Signature: sig, Example: o.msg}
			idx[sig] = c
		}
		c.Count++
	}
	var out []Cluster
	for _, c := range idx {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Signature < out[j].Signature
	})
	return out
}

// clusterAll merges per-test clusters of flaky tests into suite-wide failure clusters,
// so one shared root cause (say, a database that is slow on CI) shows up once.
func clusterAll(ts []Test) []Cluster {
	idx := map[string]*Cluster{}
	for _, t := range ts {
		if t.Verdict != FlakyV {
			continue
		}
		for _, c := range t.Messages {
			g := idx[c.Signature]
			if g == nil {
				g = &Cluster{Signature: c.Signature, Example: c.Example, Kind: kindOf(c.Example)}
				idx[c.Signature] = g
			}
			g.Count += c.Count
			g.Tests = append(g.Tests, t.Key)
		}
	}
	var out []Cluster
	for _, c := range idx {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Signature < out[j].Signature
	})
	return out
}

type rule struct {
	kind string
	re   *regexp.Regexp
}

var rules = []rule{
	{"data race", regexp.MustCompile(`(?i)data race|race condition|ConcurrentModification`)},
	{"timeout", regexp.MustCompile(`(?i)timed? ?out|deadline exceeded|exceeded.*timeout|context canceled|ETIMEDOUT|TimeoutError`)},
	{"port / address in use", regexp.MustCompile(`(?i)address already in use|EADDRINUSE|port .* (?:in use|already)`)},
	{"network", regexp.MustCompile(`(?i)connection (?:refused|reset|closed)|ECONNREFUSED|ECONNRESET|broken pipe|\bEOF\b|no such host|dns|socket hang up|503 service|502 bad gateway`)},
	{"rate limit", regexp.MustCompile(`(?i)rate.?limit|too many requests|\b429\b`)},
	{"shared state / ordering", regexp.MustCompile(`(?i)already exists|duplicate key|unique constraint|database is locked|deadlock|file exists|not empty`)},
	{"resources", regexp.MustCompile(`(?i)too many open files|out of memory|\bOOM\b|cannot allocate|no space left|ENOSPC`)},
	{"missing file / async", regexp.MustCompile(`(?i)no such file|ENOENT|not found.*yet|not ready|element .* not (?:found|visible)|detached from`)},
	{"time / randomness", regexp.MustCompile(`(?i)time\.Now|clock|expected.*\d{4}-\d\d-\d\d|random|seed`)},
}

func kindOf(msg string) string {
	for _, r := range rules {
		if r.re.MatchString(msg) {
			return r.kind
		}
	}
	return ""
}

// Diagnose guesses a cause from a test's failure messages and timing.
func Diagnose(t Test) Cause {
	counts := map[string]int{}
	for _, c := range t.Messages {
		if k := kindOf(c.Example); k != "" {
			counts[k] += c.Count
		}
	}
	best, n := "", 0
	for k, c := range counts {
		if c > n || (c == n && k < best) {
			best, n = k, c
		}
	}
	if best != "" {
		return Cause{Kind: best, Detail: "failure text matches " + best}
	}
	if t.MedianPass > 0 && t.MedianFail >= 2*t.MedianPass && t.MedianFail-t.MedianPass > 20*time.Millisecond {
		ratio := float64(t.MedianFail) / float64(t.MedianPass)
		return Cause{Kind: "timing", Detail: "slower when failing (x" + trimFloat(ratio) + ")"}
	}
	if len(t.Messages) > 1 {
		return Cause{Kind: "varied failures", Detail: "several different failure messages"}
	}
	if len(t.Messages) == 1 && t.Verdict == FlakyV {
		return Cause{Kind: "nondeterministic", Detail: "same failure, intermittently"}
	}
	return Cause{}
}

func trimFloat(f float64) string {
	s := strings.TrimRight(strings.TrimRight(strconvF(f), "0"), ".")
	return s
}

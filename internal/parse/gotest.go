package parse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/celaldrg/flakehound/internal/model"
)

type goEvent struct {
	Action  string
	Package string
	Test    string
	Elapsed float64
	Output  string
}

// parseGoTest reads `go test -json` output. Subtests are separate cases; a parent
// test that only failed because a child failed is dropped so one flaky subtest is
// not counted twice. Rerun streams (gotestsum --rerun-fails) become Flaky via Dedupe.
func parseGoTest(data []byte) ([]model.Case, error) {
	type acc struct {
		out []string
	}
	outputs := map[string]*acc{}
	var results []model.Case
	pkgFailedWithTests := map[string]bool{}
	pkgHadResult := map[string]bool{}
	pkgFail := map[string]bool{}
	pkgOut := map[string]*acc{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	lines := 0
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev goEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // go build noise before the JSON stream
		}
		lines++
		key := ev.Package + "\x00" + ev.Test
		switch ev.Action {
		case "output":
			target := outputs
			k := key
			if ev.Test == "" {
				target = pkgOut
				k = ev.Package
			}
			a := target[k]
			if a == nil {
				a = &acc{}
				target[k] = a
			}
			if len(a.out) < 400 {
				a.out = append(a.out, ev.Output)
			}
		case "pass", "fail", "skip":
			if ev.Test == "" {
				if ev.Action == "fail" {
					pkgFail[ev.Package] = true
				}
				continue
			}
			st := map[string]model.Status{"pass": model.Pass, "fail": model.Fail, "skip": model.Skip}[ev.Action]
			c := model.Case{Suite: ev.Package, Name: ev.Test, Status: st, Attempts: 1,
				Duration: time.Duration(ev.Elapsed * float64(time.Second))}
			if st == model.Fail {
				if a := outputs[key]; a != nil {
					c.Message = failMessage(a.out)
				}
				pkgFailedWithTests[ev.Package] = true
			}
			pkgHadResult[ev.Package] = true
			results = append(results, c)
			delete(outputs, key)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("gotest: %w", err)
	}
	if lines == 0 {
		return nil, fmt.Errorf("gotest: no JSON events found")
	}
	// Drop failed parents whose failure is explained by a failed child.
	failed := map[string]bool{}
	for _, c := range results {
		if c.Status == model.Fail {
			failed[c.Suite+"\x00"+c.Name] = true
		}
	}
	var out []model.Case
	for _, c := range results {
		if c.Status == model.Fail && hasFailedChild(failed, c) {
			continue
		}
		out = append(out, c)
	}
	// A package that failed without any failing test (build error, panic, timeout).
	for pkg := range pkgFail {
		explained := false
		for _, c := range out {
			if c.Suite == pkg && c.Status == model.Fail {
				explained = true
			}
		}
		if !explained {
			msg := ""
			if a := pkgOut[pkg]; a != nil {
				msg = failMessage(a.out)
			}
			out = append(out, model.Case{Suite: pkg, Name: "(package)", Status: model.Fail, Message: msg, Attempts: 1})
		}
	}
	return out, nil
}

func hasFailedChild(failed map[string]bool, c model.Case) bool {
	prefix := c.Suite + "\x00" + c.Name + "/"
	for k := range failed {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// failMessage keeps the informative lines of a test's output.
func failMessage(out []string) string {
	var keep []string
	for _, l := range strings.Split(strings.Join(out, ""), "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "=== ") || strings.HasPrefix(t, "--- ") ||
			t == "FAIL" || t == "PASS" || strings.HasPrefix(t, "FAIL\t") || strings.HasPrefix(t, "ok  \t") {
			continue
		}
		keep = append(keep, t)
	}
	return clip(strings.Join(keep, "\n"))
}

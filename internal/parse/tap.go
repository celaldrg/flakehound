package parse

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/celaldrg/flakehound/internal/model"
)

var tapResult = regexp.MustCompile(`^(not )?ok\b\s*(\d+)?\s*(?:-\s*)?(.*)$`)

// parseTAP reads TAP 13/14: "ok 1 - name", "not ok 2 - name # SKIP why",
// with indented YAML diagnostics or "# comment" lines attached to a failure.
func parseTAP(data []byte) ([]model.Case, error) {
	var out []model.Case
	var cur *model.Case
	var diag []string
	flush := func() {
		if cur != nil {
			if cur.Status == model.Fail {
				cur.Message = clip(strings.Join(diag, "\n"))
			}
			out = append(out, *cur)
		}
		cur, diag = nil, nil
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	n := 0
	for sc.Scan() {
		raw := sc.Text()
		line := strings.TrimRight(raw, "\r")
		if m := tapResult.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, " ") {
			flush()
			n++
			name := strings.TrimSpace(m[3])
			status := model.Pass
			if m[1] != "" {
				status = model.Fail
			}
			if i := strings.Index(name, " # "); i >= 0 {
				dir := strings.ToUpper(strings.TrimSpace(name[i+3:]))
				name = strings.TrimSpace(name[:i])
				switch {
				case strings.HasPrefix(dir, "SKIP"):
					status = model.Skip
				case strings.HasPrefix(dir, "TODO") && status == model.Fail:
					status = model.Skip // expected failure
				}
			}
			if name == "" {
				name = "test " + m[2]
			}
			cur = &model.Case{Name: name, Status: status, Attempts: 1}
			continue
		}
		t := strings.TrimSpace(line)
		if cur == nil || t == "" || t == "---" || t == "..." {
			continue
		}
		if strings.HasPrefix(t, "# ") {
			diag = append(diag, strings.TrimPrefix(t, "# "))
		} else if strings.HasPrefix(raw, " ") {
			diag = append(diag, t)
		}
	}
	flush()
	if n == 0 {
		return nil, fmt.Errorf("tap: no test points found")
	}
	return out, nil
}

// Package parse reads test reports (JUnit XML, go test -json, TAP, Jest JSON)
// into the common model.
package parse

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/celaldrg/flakehound/internal/model"
)

// Format names a report format.
type Format string

const (
	Auto   Format = "auto"
	JUnit  Format = "junit"
	GoTest Format = "gotest"
	TAP    Format = "tap"
	Jest   Format = "jest"
)

// Formats lists the concrete formats.
var Formats = []Format{JUnit, GoTest, TAP, Jest}

var tapLine = regexp.MustCompile(`(?m)^(not )?ok\s+\d+`)

// Detect guesses the format of a report from its content.
func Detect(data []byte) (Format, error) {
	t := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	if looksLikeGoTest(t) {
		return GoTest, nil
	}
	switch {
	case len(t) == 0:
		return "", fmt.Errorf("empty report")
	case t[0] == '<':
		return JUnit, nil
	case t[0] == '{':
		first := t
		if i := bytes.IndexByte(t, '\n'); i >= 0 {
			first = t[:i]
		}
		if bytes.Contains(first, []byte(`"Action"`)) {
			return GoTest, nil
		}
		if bytes.Contains(t[:min(len(t), 4096)], []byte(`"testResults"`)) {
			return Jest, nil
		}
		// A pretty-printed jest report starts with "{\n  \"numFailedTestSuites\"".
		if bytes.Contains(t, []byte(`"assertionResults"`)) {
			return Jest, nil
		}
	case bytes.HasPrefix(t, []byte("TAP version")), tapLine.Match(t):
		return TAP, nil
	}
	return "", fmt.Errorf("unrecognised report format (expected JUnit XML, go test -json, TAP or Jest JSON)")
}

// looksLikeGoTest finds a go test -json event in the first lines; build output
// (compiler errors, "# pkg" headers) can precede the JSON stream.
func looksLikeGoTest(t []byte) bool {
	for i := 0; i < 50 && len(t) > 0; i++ {
		line := t
		if j := bytes.IndexByte(t, '\n'); j >= 0 {
			line, t = t[:j], t[j+1:]
		} else {
			t = nil
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 && line[0] == '{' && bytes.Contains(line, []byte(`"Action"`)) {
			return true
		}
	}
	return false
}

// Parse decodes data in the given format (Auto detects it).
func Parse(data []byte, f Format) ([]model.Case, error) {
	if f == "" || f == Auto {
		d, err := Detect(data)
		if err != nil {
			return nil, err
		}
		f = d
	}
	var cs []model.Case
	var err error
	switch f {
	case JUnit:
		cs, err = parseJUnit(data)
	case GoTest:
		cs, err = parseGoTest(data)
	case TAP:
		cs, err = parseTAP(data)
	case Jest:
		cs, err = parseJest(data)
	default:
		return nil, fmt.Errorf("unknown format %q", f)
	}
	if err != nil {
		return nil, err
	}
	return model.Dedupe(cs), nil
}

// clip shortens a failure message to something storable and printable.
func clip(s string) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, "?"))
	limit := 600
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit] + "…"
}

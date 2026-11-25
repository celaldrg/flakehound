package parse

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/celaldrg/flakehound/internal/model"
)

type jProblem struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

func (p jProblem) msg() string {
	m := strings.TrimSpace(p.Message)
	t := strings.TrimSpace(p.Text)
	switch {
	case m == "":
		m = t
	case t != "" && !strings.HasPrefix(t, m):
		m += "\n" + t
	}
	if m == "" {
		m = p.Type
	}
	return m
}

type jCase struct {
	Name      string     `xml:"name,attr"`
	Classname string     `xml:"classname,attr"`
	File      string     `xml:"file,attr"`
	Time      string     `xml:"time,attr"`
	Failure   []jProblem `xml:"failure"`
	Error     []jProblem `xml:"error"`
	Skipped   []jProblem `xml:"skipped"`
	// Maven Surefire / Gradle rerun support: the test failed N times, then passed.
	FlakyFailure []jProblem `xml:"flakyFailure"`
	FlakyError   []jProblem `xml:"flakyError"`
	// ... or failed on every rerun.
	RerunFailure []jProblem `xml:"rerunFailure"`
	RerunError   []jProblem `xml:"rerunError"`
}

func parseJUnit(data []byte) ([]model.Case, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	var out []model.Case
	var suiteStack []string
	seenRoot := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("junit: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "testsuites", "testsuite":
				seenRoot = true
				name := ""
				for _, a := range t.Attr {
					if a.Name.Local == "name" {
						name = a.Value
					}
				}
				suiteStack = append(suiteStack, name)
			case "testcase":
				var jc jCase
				if err := dec.DecodeElement(&jc, &t); err != nil {
					return nil, fmt.Errorf("junit: %w", err)
				}
				out = append(out, convertJCase(jc, suiteStack))
			}
		case xml.EndElement:
			if (t.Name.Local == "testsuite" || t.Name.Local == "testsuites") && len(suiteStack) > 0 {
				suiteStack = suiteStack[:len(suiteStack)-1]
			}
		}
	}
	if !seenRoot {
		return nil, fmt.Errorf("junit: no <testsuite> element found")
	}
	return out, nil
}

func convertJCase(jc jCase, stack []string) model.Case {
	c := model.Case{Name: jc.Name, Suite: jc.Classname, Status: model.Pass, Attempts: 1}
	if c.Suite == "" {
		c.Suite = jc.File
	}
	if c.Suite == "" && len(stack) > 0 {
		c.Suite = stack[len(stack)-1]
	}
	if f, err := strconv.ParseFloat(jc.Time, 64); err == nil && f >= 0 {
		c.Duration = time.Duration(f * float64(time.Second))
	}
	switch {
	case len(jc.Failure)+len(jc.Error) > 0:
		c.Status = model.Fail
		if len(jc.Failure) > 0 {
			c.Message = jc.Failure[0].msg()
		} else {
			c.Message = jc.Error[0].msg()
		}
		c.Attempts += len(jc.RerunFailure) + len(jc.RerunError)
	case len(jc.Skipped) > 0:
		c.Status = model.Skip
	case len(jc.FlakyFailure)+len(jc.FlakyError) > 0:
		c.Status = model.Flaky
		if len(jc.FlakyFailure) > 0 {
			c.Message = jc.FlakyFailure[0].msg()
		} else {
			c.Message = jc.FlakyError[0].msg()
		}
		c.Attempts += len(jc.FlakyFailure) + len(jc.FlakyError)
	case len(jc.RerunFailure)+len(jc.RerunError) > 0:
		c.Status = model.Fail
		if len(jc.RerunFailure) > 0 {
			c.Message = jc.RerunFailure[0].msg()
		} else {
			c.Message = jc.RerunError[0].msg()
		}
		c.Attempts += len(jc.RerunFailure) + len(jc.RerunError)
	}
	c.Message = clip(c.Message)
	return c
}

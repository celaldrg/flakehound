package parse

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/celaldrg/flakehound/internal/model"
)

type jestReport struct {
	TestResults []struct {
		Name             string `json:"name"`
		AssertionResults []struct {
			FullName        string   `json:"fullName"`
			Title           string   `json:"title"`
			Status          string   `json:"status"`
			Duration        *float64 `json:"duration"`
			FailureMessages []string `json:"failureMessages"`
			Retries         int      `json:"invocations"`
		} `json:"assertionResults"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"testResults"`
}

func parseJest(data []byte) ([]model.Case, error) {
	var r jestReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("jest: %w", err)
	}
	if r.TestResults == nil {
		return nil, fmt.Errorf("jest: no testResults array")
	}
	var out []model.Case
	for _, f := range r.TestResults {
		if len(f.AssertionResults) == 0 && f.Status == "failed" {
			// the file failed to run at all (syntax error, import error)
			out = append(out, model.Case{Suite: f.Name, Name: "(test suite failed to run)",
				Status: model.Fail, Message: clip(f.Message), Attempts: 1})
		}
		for _, a := range f.AssertionResults {
			name := a.FullName
			if name == "" {
				name = a.Title
			}
			c := model.Case{Suite: f.Name, Name: name, Attempts: 1}
			if a.Duration != nil {
				c.Duration = time.Duration(*a.Duration * float64(time.Millisecond))
			}
			switch a.Status {
			case "passed":
				c.Status = model.Pass
				if a.Retries > 1 { // jest.retryTimes: passed after retries
					c.Status = model.Flaky
					c.Attempts = a.Retries
				}
			case "failed":
				c.Status = model.Fail
				c.Message = clip(strings.Join(a.FailureMessages, "\n"))
			default:
				c.Status = model.Skip
			}
			out = append(out, c)
		}
	}
	return out, nil
}

package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/celaldrg/flakehound/internal/analyze"
	"github.com/celaldrg/flakehound/internal/model"
)

func sample() analyze.Report {
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	var runs []model.Run
	for i := 0; i < 10; i++ {
		st := model.Pass
		if i%3 == 0 {
			st = model.Fail
		}
		runs = append(runs, model.Run{ID: fmt.Sprint(i), Label: "abc", Time: t0.Add(time.Duration(i) * time.Minute),
			Cases: []model.Case{
				{Suite: "pay", Name: "TestCharge", Status: st, Message: "dial tcp 127.0.0.1:99: connection refused", Duration: time.Duration(i+1) * time.Millisecond},
				{Suite: "pay", Name: "TestOK", Status: model.Pass},
				{Suite: "pay", Name: "TestBug", Status: model.Fail, Message: "nope"},
			}})
	}
	return analyze.Analyze(runs, analyze.Options{})
}

func TestTextPlain(t *testing.T) {
	var b bytes.Buffer
	Text(&b, sample(), Options{})
	out := b.String()
	for _, want := range []string{"10 runs", "1 flaky", "1 broken", "pay::TestCharge", "pay::TestBug", "[network]", "chance of a red build", "Failure clusters", "flakehound quarantine"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("plain style must not emit ANSI")
	}
	if strings.Contains(out, "pay::TestOK") {
		t.Error("stable tests are hidden by default")
	}
}

func TestTextColorAndAll(t *testing.T) {
	var b bytes.Buffer
	Text(&b, sample(), Options{All: true, Style: Style{Color: true}})
	if !strings.Contains(b.String(), "\x1b[") {
		t.Error("expected ANSI")
	}
}

func TestTextEmpty(t *testing.T) {
	var b bytes.Buffer
	Text(&b, analyze.Analyze(nil, analyze.Options{}), Options{})
	if !strings.Contains(b.String(), "No runs in history") {
		t.Errorf("%s", b.String())
	}
}

func TestTextLimit(t *testing.T) {
	var runs []model.Run
	t0 := time.Now()
	for i := 0; i < 6; i++ {
		var cs []model.Case
		for j := 0; j < 5; j++ {
			st := model.Pass
			if (i+j)%2 == 0 {
				st = model.Fail
			}
			cs = append(cs, model.Case{Name: fmt.Sprintf("T%d", j), Status: st})
		}
		runs = append(runs, model.Run{ID: fmt.Sprint(i), Label: "x", Time: t0.Add(time.Duration(i)), Cases: cs})
	}
	var b bytes.Buffer
	Text(&b, analyze.Analyze(runs, analyze.Options{}), Options{Limit: 2})
	if !strings.Contains(b.String(), "and 3 more") {
		t.Errorf("%s", b.String())
	}
}

func TestMarkdown(t *testing.T) {
	var b bytes.Buffer
	Markdown(&b, sample(), Options{})
	out := b.String()
	for _, want := range []string{"## flakehound", "| Rate |", "`pay::TestCharge`", "**Broken:** `pay::TestBug`"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestJSON(t *testing.T) {
	var b bytes.Buffer
	if err := JSON(&b, sample(), false); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Runs   int
		Counts map[string]int
		Tests  []struct {
			Key     string
			Verdict string
			Fails   int
			Cause   string
		}
	}
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Runs != 10 || got.Counts["flaky"] != 1 || len(got.Tests) != 2 {
		t.Errorf("%+v", got)
	}
	var all bytes.Buffer
	JSON(&all, sample(), true)
	if !strings.Contains(all.String(), "pay::TestOK") {
		t.Error("-all should include stable tests")
	}
}

func TestHelpers(t *testing.T) {
	if pct(0) != "0%" || pct(0.004) != "<1%" || pct(0.5) != "50%" || pct(0.999) != "100%" {
		t.Error("pct")
	}
	if trunc("héllo wörld", 6) != "héllo…" || trunc("abc", 6) != "abc" {
		t.Error("trunc")
	}
	if dur(0) != "-" || dur(1500*time.Millisecond) != "1.5s" || dur(20*time.Millisecond) != "20ms" || dur(5*time.Microsecond) != "5µs" {
		t.Error("dur")
	}
	if pad("é", 3) != "é  " {
		t.Error("pad must count runes")
	}
}

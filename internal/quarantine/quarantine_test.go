package quarantine

import (
	"bytes"
	"strings"
	"testing"

	"github.com/celaldrg/flakehound/internal/analyze"
)

func tests() []analyze.Test {
	return []analyze.Test{
		{Key: "x/pay::TestCharge", Suite: "x/pay", Name: "TestCharge", Fails: 3, Runs: 10, Rate: 0.3, Verdict: analyze.FlakyV},
		{Key: "x/pay::TestTable/b c", Suite: "x/pay", Name: "TestTable/b c", Fails: 1, Runs: 10, Rate: 0.1, Verdict: analyze.FlakyV},
		{Key: "x/pay::(package)", Suite: "x/pay", Name: "(package)", Rate: 0.5, Verdict: analyze.FlakyV},
	}
}

func TestReadWrite(t *testing.T) {
	var b bytes.Buffer
	Write(&b, tests())
	s, err := Read(&b)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 3 || !s["x/pay::TestCharge"] || !s["x/pay::TestTable/b c"] {
		t.Errorf("%v", s)
	}
}

func TestReadCommentsAndBlank(t *testing.T) {
	s, _ := Read(strings.NewReader("# header\n\n  a::b  # owner: me\nc::d\n"))
	if len(s) != 2 || !s["a::b"] || !s["c::d"] {
		t.Errorf("%v", s)
	}
}

func TestSelect(t *testing.T) {
	rep := analyze.Report{Tests: []analyze.Test{
		{Key: "a", Rate: 0.5, Verdict: analyze.FlakyV},
		{Key: "b", Rate: 0.01, Verdict: analyze.FlakyV},
		{Key: "c", Rate: 1, Verdict: analyze.Broken},
		{Key: "d", Rate: 0, Verdict: analyze.Stable},
	}}
	got := Select(rep, 0.02)
	if len(got) != 1 || got[0].Key != "a" {
		t.Errorf("%+v", got)
	}
}

func TestRenderGo(t *testing.T) {
	got, err := Render("go", tests())
	if err != nil {
		t.Fatal(err)
	}
	want := `-skip '^(TestCharge|TestTable/b c)$'`
	if got != want {
		t.Errorf("got %s", got)
	}
	empty, _ := Render("go", nil)
	if empty != "" {
		t.Errorf("empty: %q", empty)
	}
}

func TestRenderGoEscapes(t *testing.T) {
	got, _ := Render("go", []analyze.Test{{Name: "TestA/x.y(1)"}})
	if got != `-skip '^(TestA/x\.y\(1\))$'` {
		t.Errorf("got %s", got)
	}
}

func TestRenderList(t *testing.T) {
	got, _ := Render("list", tests())
	if strings.Count(got, "\n") != 2 || !strings.HasPrefix(got, "x/pay::(package)") {
		t.Errorf("got %q", got)
	}
}

func TestRenderPytest(t *testing.T) {
	ts := []analyze.Test{
		{Suite: "tests.test_pay.TestCharge", Name: "test_card"},
		{Suite: "tests/test_a.py", Name: "test_b's"},
	}
	got, _ := Render("pytest", ts)
	if !strings.Contains(got, `--deselect 'tests/test_pay.py::TestCharge::test_card'`) {
		t.Errorf("classname form: %s", got)
	}
	if !strings.Contains(got, `--deselect 'tests/test_a.py::test_b'\''s'`) {
		t.Errorf("path form / quoting: %s", got)
	}
}

func TestRenderJest(t *testing.T) {
	got, _ := Render("jest", []analyze.Test{{Name: "a fetches (slow)"}})
	if !strings.HasPrefix(got, "--testNamePattern '^(?!") || !strings.Contains(got, `a fetches \(slow\)`) {
		t.Errorf("got %s", got)
	}
}

func TestRenderUnknown(t *testing.T) {
	if _, err := Render("nope", nil); err == nil {
		t.Error("expected error")
	}
}

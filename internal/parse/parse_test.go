package parse

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/celaldrg/flakehound/internal/model"
)

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func byKey(cs []model.Case) map[string]model.Case {
	m := map[string]model.Case{}
	for _, c := range cs {
		m[c.Key()] = c
	}
	return m
}

func TestDetect(t *testing.T) {
	tests := []struct {
		file string
		want Format
	}{{"junit.xml", JUnit}, {"gotest.jsonl", GoTest}, {"tap.txt", TAP}, {"jest.json", Jest}}
	for _, tc := range tests {
		got, err := Detect(load(t, tc.file))
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v, %v want %v", tc.file, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "   \n", "hello world", `{"foo":1}`, "[1,2]"} {
		if _, err := Detect([]byte(bad)); err == nil {
			t.Errorf("Detect(%q) should fail", bad)
		}
	}
}

func TestDetectBOM(t *testing.T) {
	b := append([]byte("\xef\xbb\xbf"), []byte(`<testsuite><testcase name="x"/></testsuite>`)...)
	if f, err := Detect(b); err != nil || f != JUnit {
		t.Fatalf("got %v %v", f, err)
	}
}

func TestJUnit(t *testing.T) {
	cs, err := Parse(load(t, "junit.xml"), Auto)
	if err != nil {
		t.Fatal(err)
	}
	m := byKey(cs)
	if len(cs) != 6 {
		t.Fatalf("got %d cases", len(cs))
	}
	if c := m["pkg.OrderTest::creates_order"]; c.Status != model.Pass || c.Duration != 120*time.Millisecond {
		t.Errorf("creates_order: %+v", c)
	}
	if c := m["pkg.OrderTest::cancels_order"]; c.Status != model.Fail || !strings.Contains(c.Message, "expected 3 but was 4") || !strings.Contains(c.Message, "OrderTest.java:42") {
		t.Errorf("cancels_order: %+v", c)
	}
	if c := m["pkg.OrderTest::pays_order"]; c.Status != model.Fail || !strings.Contains(c.Message, "Connection refused") {
		t.Errorf("pays_order (error element): %+v", c)
	}
	if c := m["pkg.OrderTest::ships_order"]; c.Status != model.Skip {
		t.Errorf("ships_order: %+v", c)
	}
	if c := m["pkg.OrderTest::rescued_by_retry"]; c.Status != model.Flaky || c.Attempts != 3 || !strings.Contains(c.Message, "timeout") {
		t.Errorf("rescued_by_retry: %+v", c)
	}
	// no classname falls back to the enclosing testsuite name; bad time is tolerated
	if c, ok := m["other::no_classname"]; !ok || c.Duration != 0 {
		t.Errorf("no_classname: %+v ok=%v", c, ok)
	}
}

func TestJUnitRerunFailure(t *testing.T) {
	x := `<testsuite name="s"><testcase classname="c" name="t"><failure message="boom"/><rerunFailure message="boom"/><rerunFailure message="boom"/></testcase>
	<testcase classname="c" name="u"><rerunError message="still bad"/></testcase></testsuite>`
	cs, err := Parse([]byte(x), JUnit)
	if err != nil {
		t.Fatal(err)
	}
	m := byKey(cs)
	if c := m["c::t"]; c.Status != model.Fail || c.Attempts != 3 {
		t.Errorf("t: %+v", c)
	}
	if c := m["c::u"]; c.Status != model.Fail || c.Message != "still bad" {
		t.Errorf("u: %+v", c)
	}
}

func TestJUnitMultipleRoots(t *testing.T) {
	x := `<?xml version="1.0"?><testsuite name="a"><testcase name="x"/></testsuite>
<?xml version="1.0"?><testsuite name="b"><testcase name="y"><failure/></testcase></testsuite>`
	cs, err := Parse([]byte(x), JUnit)
	if err != nil || len(cs) != 2 {
		t.Fatalf("%v %v", cs, err)
	}
	if c := byKey(cs)["b::y"]; c.Status != model.Fail {
		t.Errorf("%+v", c)
	}
}

func TestJUnitErrors(t *testing.T) {
	for _, bad := range []string{`<html><body>hi</body></html>`, `<testsuite><testcase name="x"`, `<foo/>`} {
		if _, err := Parse([]byte(bad), JUnit); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestGoTest(t *testing.T) {
	cs, err := Parse(load(t, "gotest.jsonl"), Auto)
	if err != nil {
		t.Fatal(err)
	}
	m := byKey(cs)
	if c := m["x/pay::TestCharge"]; c.Status != model.Fail || !strings.Contains(c.Message, "connection refused") || strings.Contains(c.Message, "--- FAIL") {
		t.Errorf("TestCharge: %+v", c)
	}
	if _, ok := m["x/pay::TestTable"]; ok {
		t.Error("parent of failed subtest should be dropped")
	}
	if c := m["x/pay::TestTable/b"]; c.Status != model.Fail || !strings.Contains(c.Message, "got 2 want 3") {
		t.Errorf("subtest: %+v", c)
	}
	if c := m["x/pay::TestTable/a"]; c.Status != model.Pass {
		t.Errorf("subtest a: %+v", c)
	}
	if c := m["x/pay::TestOK"]; c.Status != model.Pass || c.Duration != 10*time.Millisecond {
		t.Errorf("TestOK: %+v", c)
	}
	if c := m["x/pay::TestSkipped"]; c.Status != model.Skip {
		t.Errorf("TestSkipped: %+v", c)
	}
	if c := m["x/pay::TestRetried"]; c.Status != model.Flaky || c.Attempts != 2 {
		t.Errorf("TestRetried (rerun stream): %+v", c)
	}
	if c := m["x/broken::(package)"]; c.Status != model.Fail || !strings.Contains(c.Message, "panic: boom") {
		t.Errorf("package failure: %+v", c)
	}
	if _, ok := m["x/pay::(package)"]; ok {
		t.Error("explained package failure must not add a pseudo test")
	}
}

func TestGoTestNoEvents(t *testing.T) {
	if _, err := Parse([]byte("ok  \tfoo\t0.1s\n"), GoTest); err == nil {
		t.Error("expected error")
	}
}

func TestTAP(t *testing.T) {
	cs, err := Parse(load(t, "tap.txt"), Auto)
	if err != nil {
		t.Fatal(err)
	}
	m := byKey(cs)
	if len(cs) != 6 {
		t.Fatalf("got %d: %+v", len(cs), cs)
	}
	if c := m["parses numbers"]; c.Status != model.Pass {
		t.Errorf("%+v", c)
	}
	if c := m["handles unicode"]; c.Status != model.Fail || !strings.Contains(c.Message, "lib/text.js:88") {
		t.Errorf("yaml diag: %+v", c)
	}
	if c := m["slow thing"]; c.Status != model.Skip {
		t.Errorf("skip: %+v", c)
	}
	if c := m["not implemented"]; c.Status != model.Skip {
		t.Errorf("todo: %+v", c)
	}
	if c := m["connects"]; c.Status != model.Fail || !strings.Contains(c.Message, "ECONNRESET") {
		t.Errorf("comment diag: %+v", c)
	}
	if _, ok := m["test 6"]; !ok {
		t.Error("unnamed test point should get a generated name")
	}
}

func TestTAPEmpty(t *testing.T) {
	if _, err := Parse([]byte("TAP version 13\n1..0\n"), TAP); err == nil {
		t.Error("expected error")
	}
}

func TestJest(t *testing.T) {
	cs, err := Parse(load(t, "jest.json"), Auto)
	if err != nil {
		t.Fatal(err)
	}
	m := byKey(cs)
	if c := m["/repo/src/a.test.js::a fetches"]; c.Status != model.Fail || !strings.Contains(c.Message, "Exceeded timeout") || c.Duration < 5*time.Second {
		t.Errorf("fetches: %+v", c)
	}
	if c := m["/repo/src/a.test.js::a later"]; c.Status != model.Skip {
		t.Errorf("pending: %+v", c)
	}
	if c := m["/repo/src/a.test.js::a retried"]; c.Status != model.Flaky || c.Attempts != 3 {
		t.Errorf("retried: %+v", c)
	}
	if c := m["/repo/src/b.test.js::(test suite failed to run)"]; c.Status != model.Fail || !strings.Contains(c.Message, "SyntaxError") {
		t.Errorf("suite failure: %+v", c)
	}
}

func TestJestInvalid(t *testing.T) {
	for _, bad := range []string{`{"a":`, `{"testResults": null}`} {
		if _, err := Parse([]byte(bad), Jest); err == nil {
			t.Errorf("expected error for %s", bad)
		}
	}
}

func TestUnknownFormat(t *testing.T) {
	if _, err := Parse([]byte("x"), "weird"); err == nil {
		t.Error("expected error")
	}
}

func TestClipUTF8(t *testing.T) {
	s := strings.Repeat("İ", 400) // 2 bytes each
	got := clip(s)
	if !strings.HasSuffix(got, "…") || strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("clip produced invalid text: %q", got[len(got)-10:])
	}
}

func TestDedupe(t *testing.T) {
	in := []model.Case{
		{Name: "a", Status: model.Fail, Message: "x"},
		{Name: "a", Status: model.Pass},
		{Name: "b", Status: model.Fail, Message: "1"},
		{Name: "b", Status: model.Fail, Message: "2"},
		{Name: "c", Status: model.Skip},
		{Name: "c", Status: model.Pass},
		{Name: "d", Status: model.Pass},
		{Name: "d", Status: model.Pass},
	}
	m := byKey(model.Dedupe(in))
	if m["a"].Status != model.Flaky || m["a"].Attempts != 2 || m["a"].Message != "x" {
		t.Errorf("a: %+v", m["a"])
	}
	if m["b"].Status != model.Fail || m["b"].Message != "2" || m["b"].Attempts != 2 {
		t.Errorf("b: %+v", m["b"])
	}
	if m["c"].Status != model.Pass {
		t.Errorf("c: %+v", m["c"])
	}
	if m["d"].Status != model.Pass || m["d"].Attempts != 2 {
		t.Errorf("d: %+v", m["d"])
	}
}

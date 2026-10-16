package clierrors

import (
	"errors"
	"strings"
	"testing"
)

func TestExitCodeMapping(t *testing.T) {
	cases := map[Code]int{
		CodeInternal: 1, CodeUsage: 2, CodeConfirm: 3,
		CodeDevice: 4, CodeProtocol: 5, CodeTimeout: 6, CodeConflict: 7,
	}
	for code, want := range cases {
		e := New(code, "boom")
		if e.ExitCode() != want {
			t.Errorf("%s exit=%d want %d", code, e.ExitCode(), want)
		}
	}
}

func TestLinesIncludeHintAndValid(t *testing.T) {
	e := New(CodeUsage, "unknown color %q", "lime").
		WithHint("named colors below").
		WithValid("red", "green")
	lines := strings.Join(e.Lines(), "\n")
	for _, probe := range []string{"unknown color \"lime\"", "hint: named colors below", "valid: red, green"} {
		if !strings.Contains(lines, probe) {
			t.Fatalf("missing %q in %q", probe, lines)
		}
	}
}

func TestErrStringWrapsCause(t *testing.T) {
	e := New(CodeDevice, "read failed").WithCause(errors.New("permission denied"))
	if !strings.Contains(e.Error(), "read failed: permission denied") {
		t.Fatalf("wrap failed: %s", e.Error())
	}
	if got := errors.Unwrap(e); got != nil && got.Error() != "permission denied" {
		t.Fatalf("unwrap: %s", got)
	}
}

func TestAllCodesCovered(t *testing.T) {
	all := All()
	for _, c := range []Code{CodeInternal, CodeUsage, CodeConfirm, CodeDevice, CodeProtocol, CodeTimeout, CodeConflict} {
		if _, ok := all[c]; !ok {
			t.Fatalf("code %s missing from All()", c)
		}
	}
	if len(all) != 7 {
		t.Fatalf("unexpected extra codes: %v", all)
	}
}

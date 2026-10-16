package output

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
)

func render(t *testing.T, jsonMode bool, deliver string, o Outcome) (stdout, stderr string, exit int) {
	t.Helper()
	var out, errb bytes.Buffer
	r := New(jsonMode, deliver)
	r.Stdout = &out
	r.Stderr = &errb
	exit = r.Finish(o)
	return out.String(), errb.String(), exit
}

func TestJSONSuccessEnvelopeOnStdout(t *testing.T) {
	stdout, stderr, exit := render(t, true, "", Outcome{
		Command: "discover", Op: "device.discover",
		Human: []string{"should-not-appear-in-json"},
		Data:  []byte(`{"x":1}`),
	})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr should be empty: %q", stderr)
	}
	var env Envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout not an envelope: %q", stdout)
	}
	if !env.OK || env.Command != "discover" || env.Op != "device.discover" || env.SchemaVersion != SchemaVersion {
		t.Fatalf("bad envelope: %+v", env)
	}
	if strings.Contains(stdout, "should-not-appear") {
		t.Fatal("human lines leaked into json mode")
	}
}

func TestJSONErrorEnvelopeCarriesHintAndValid(t *testing.T) {
	err := clierrors.New(clierrors.CodeUsage, "unknown property %q", "hugs").
		WithValid("power", "brightness")
	stdout, stderr, exit := render(t, true, "", Outcome{Command: "set", Err: err})
	if exit != 2 {
		t.Fatalf("usage exit expected 2, got %d", exit)
	}
	if stderr != "" {
		t.Fatalf("errors land in the envelope in json mode: %q", stderr)
	}
	var env Envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if env.OK || env.Error == nil || env.Error.Code != "usage" || len(env.Error.Valid) != 2 {
		t.Fatalf("bad error envelope: %+v %+v", env.OK, env.Error)
	}
}

func TestHumanErrorGoesToStderr(t *testing.T) {
	err := clierrors.New(clierrors.CodeConfirm, "confirmation required").
		WithHint("pass --yes")
	stdout, stderr, exit := render(t, false, "", Outcome{Command: "profile rm", Err: err})
	if exit != 3 {
		t.Fatalf("confirm exit expected 3, got %d", exit)
	}
	if stdout != "" {
		t.Fatalf("no stdout expected: %q", stdout)
	}
	if !strings.Contains(stderr, "error:") || !strings.Contains(stderr, "hint: pass --yes") {
		t.Fatalf("stderr should carry error+hint: %q", stderr)
	}
}

func TestHumanSuccessOnStdoutWarningsOnStderr(t *testing.T) {
	stdout, stderr, exit := render(t, false, "", Outcome{
		Human:    []string{"row one", "row two"},
		Warnings: []string{"be careful"},
	})
	if exit != 0 || !strings.Contains(stdout, "row one\nrow two\n") {
		t.Fatalf("stdout rows: exit=%d %q", exit, stdout)
	}
	if !strings.Contains(stderr, "warning: be careful") {
		t.Fatalf("warnings: %q", stderr)
	}
}

func TestDeliverFileRoutesEnvelope(t *testing.T) {
	path := t.TempDir() + "/artifact.json"
	stdout, stderr, exit := render(t, false, "file:"+path, Outcome{
		Human: []string{"human row"},
		Data:  []byte(`{"k":"v"}`),
	})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	if strings.Contains(stdout, "human row") {
		t.Fatalf("stdout should not duplicate the artifact: %q", stdout)
	}
	blob, ferr := os.ReadFile(path)
	if ferr != nil {
		t.Fatalf("artifact read: %v", ferr)
	}
	var env Envelope
	if err := json.Unmarshal(blob, &env); err != nil {
		t.Fatalf("artifact not an envelope: %s", string(blob))
	}
	if env.OK != true || strings.Contains(string(env.Data), "v") != true {
		t.Fatalf("artifact content: %s", string(blob))
	}
}

func TestParseDeliver(t *testing.T) {
	modes := map[string]string{
		"stdout":                     "stdout",
		"file:/tmp/x.json":           "file",
		"webhook:http://example.com": "webhook",
	}
	for spec, want := range modes {
		mode, _, err := ParseDeliver(spec)
		if err != nil || mode != want {
			t.Errorf("spec %q → (%q, %v)", spec, mode, err)
		}
	}
	if _, _, err := ParseDeliver("carrier-pigeon:out"); err == nil {
		t.Fatal("unknown deliver spec accepted")
	}
	if _, _, err := ParseDeliver("file:"); err == nil {
		t.Fatal("empty file path accepted")
	}
	if _, _, err := ParseDeliver("webhook:ftp://ex.com"); err == nil {
		t.Fatal("non-http webhook accepted")
	}
}

func TestTableAlignment(t *testing.T) {
	lines := Table([]string{"A", "BC"}, [][]string{{"1", "2"}})
	if len(lines) != 2 {
		t.Fatalf("expected header+row: %q", lines)
	}
	if len(lines[0]) < len(lines[1]) {
		t.Fatalf("header shorter than row? %q vs %q", lines[0], lines[1])
	}
}

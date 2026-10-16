// Package output owns the response contract:
//
//	stdout = payload only (human table OR one JSON envelope)
//	stderr = warnings, notes, errors
//	--json selects envelope mode; --deliver redirects the JSON artifact.
//
// The envelope is a byte-stable agent contract: {"ok":..,"command":..,"op":..,
// "schema_version":..,"data":..,"warnings":[..],"error":{code,message,hint,valid}}.
package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
)

// SchemaVersion is bumped whenever envelope field meanings change.
const SchemaVersion = 1

// Envelope is the single JSON shape for every data and error response.
type Envelope struct {
	OK            bool            `json:"ok"`
	Command       string          `json:"command"`
	Op            string          `json:"op,omitempty"`
	SchemaVersion int             `json:"schema_version"`
	Data          json.RawMessage `json:"data,omitempty"`
	Warnings      []string        `json:"warnings,omitempty"`
	Error         *ErrEnvelope    `json:"error,omitempty"`
}

type ErrEnvelope struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Hint    string   `json:"hint,omitempty"`
	Valid   []string `json:"valid,omitempty"`
}

// Runtime captures what the process knows about its own I/O environment.
type Runtime struct {
	StdinTTY  bool
	StdoutTTY bool
}

type Renderer struct {
	JSON    bool
	Deliver string // "stdout" | "file:<path>" | "webhook:<url>"
	Runtime

	Stdout io.Writer
	Stderr io.Writer
}

func New(jsonMode bool, deliver string) Renderer {
	r := Renderer{
		JSON:    jsonMode,
		Deliver: deliver,
		Runtime: Runtime{StdinTTY: IsTTY(os.Stdin), StdoutTTY: IsTTY(os.Stdout)},
	}
	if r.Stdout == nil {
		r.Stdout = os.Stdout
	}
	if r.Stderr == nil {
		r.Stderr = os.Stderr
	}
	return r
}

// Outcome is what a command returns; Finish renders it and returns the exit code.
type Outcome struct {
	Command  string
	Op       string
	Human    []string // stdout lines in human mode
	Data     json.RawMessage
	Err      error
	Warnings []string
}

// Finish renders the outcome. stdout receives the payload (human lines or the
// JSON envelope); stderr receives warnings; errors render to stderr in human
// mode and inside the envelope in JSON mode. Exit code is returned, not exited.
func (r Renderer) Finish(o Outcome) int {
	errCode := -1
	var ce *clierrors.Error
	if o.Err != nil {
		if !errors.As(o.Err, &ce) {
			ce = clierrors.New(clierrors.CodeInternal, "%v", o.Err)
		}
		errCode = ce.ExitCode()
	}

	env := Envelope{Command: o.Command, Op: o.Op, SchemaVersion: SchemaVersion, OK: errCode < 0}
	if len(o.Warnings) > 0 {
		env.Warnings = o.Warnings
	}
	if len(o.Data) > 0 {
		env.Data = o.Data
	}
	if errCode >= 0 {
		env.Error = &ErrEnvelope{
			Code:    string(ce.Code),
			Message: ce.Message,
			Hint:    ce.Hint,
			Valid:   ce.Valid,
		}
	}

	for _, w := range o.Warnings {
		fmt.Fprintln(r.Stderr, "warning: "+w)
	}

	// Artifact routing: file/webhook receive the JSON envelope; stdout stays
	// for humans (a short notice goes to stderr). Delivery problems surface
	// as real command failures.
	routedElsewhere := false
	mode, target, derr := ParseDeliver(r.Deliver)
	if derr != nil {
		return r.renderError(clierrors.New(clierrors.CodeUsage, "%v", derr), o.Command)
	}
	if mode != "stdout" && errCode < 0 {
		body, merr := json.MarshalIndent(env, "", "  ")
		if merr != nil {
			return r.renderError(clierrors.New(clierrors.CodeInternal, "marshal envelope").WithCause(merr), o.Command)
		}
		switch mode {
		case "file":
			if werr := os.WriteFile(target, body, 0o600); werr != nil {
				return r.renderError(clierrors.New(clierrors.CodeDevice, "write artifact %s", target).WithCause(werr).
					WithHint("create the parent directory first, or use --deliver stdout"), o.Command)
			}
			fmt.Fprintf(r.Stderr, "wrote %d bytes to %s\n", len(body), target)
		case "webhook":
			client := &http.Client{Timeout: 15 * time.Second}
			resp, perr := client.Post(target, "application/json", strings.NewReader(string(body)))
			if perr != nil {
				return r.renderError(clierrors.New(clierrors.CodeDevice, "deliver via webhook %s failed", target).WithCause(perr).
					WithHint("check the URL and network; use --deliver stdout to see the artifact directly"), o.Command)
			}
			resp.Body.Close()
			fmt.Fprintf(r.Stderr, "delivered %d bytes to %s (HTTP %d)\n", len(body), target, resp.StatusCode)
		}
		routedElsewhere = true
	}

	switch {
	case r.JSON:
		if !routedElsewhere {
			b, _ := json.MarshalIndent(env, "", "  ")
			fmt.Fprintln(r.Stdout, string(b))
		}
		if errCode < 0 {
			return 0
		}
		return errCode
	case errCode >= 0:
		for _, line := range ce.Lines() {
			fmt.Fprintln(r.Stderr, line)
		}
		return errCode
	case routedElsewhere:
		return 0
	default:
		for _, line := range o.Human {
			fmt.Fprintln(r.Stdout, line)
		}
		return 0
	}
}

func (r Renderer) renderError(err error, command string) int {
	var ce *clierrors.Error
	if !errors.As(err, &ce) {
		ce = clierrors.New(clierrors.CodeInternal, "%v", err)
	}
	if r.JSON {
		env := Envelope{Command: command, SchemaVersion: SchemaVersion}
		env.Error = &ErrEnvelope{Code: string(ce.Code), Message: ce.Message, Hint: ce.Hint, Valid: ce.Valid}
		b, _ := json.MarshalIndent(env, "", "  ")
		fmt.Fprintln(r.Stdout, string(b))
		return ce.ExitCode()
	}
	for _, line := range ce.Lines() {
		fmt.Fprintln(r.Stderr, line)
	}
	return ce.ExitCode()
}

// ParseDeliver splits a --deliver spec into mode + target.
func ParseDeliver(spec string) (mode, target string, err error) {
	switch {
	case spec == "" || spec == "stdout":
		return "stdout", "", nil
	case strings.HasPrefix(spec, "file:"):
		rest := strings.TrimPrefix(spec, "file:")
		if rest == "" {
			return "", "", errors.New("--deliver file: needs a path, e.g. file:/tmp/artifact.json")
		}
		return "file", expandHome(rest), nil
	case strings.HasPrefix(spec, "webhook:"):
		rest := strings.TrimPrefix(spec, "webhook:")
		u, uerr := url.Parse(rest)
		if uerr != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return "", "", fmt.Errorf("--deliver webhook: needs an http(s) URL, got %q", rest)
		}
		return "webhook", rest, nil
	default:
		return "", "", fmt.Errorf("unknown --deliver target %q; valid: stdout, file:<path>, webhook:<url>", spec)
	}
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// Table renders an aligned plain-text table for human mode. data-only mode:
// headers optional (pass nil).
func Table(headers []string, rows [][]string) []string {
	if len(rows) == 0 {
		return nil
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	lines := make([]string, 0, len(rows)+len(headers))
	if len(headers) > 0 {
		lines = append(lines, align(widths, headers))
		for _, row := range rows {
			lines = append(lines, align(widths, row))
		}
	}
	return lines
}

func align(widths []int, cells []string) string {
	var sb strings.Builder
	for i, w := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		sb.WriteString(cell)
		sb.WriteString(strings.Repeat(" ", w-len(cell)+2))
	}
	return strings.TrimRight(sb.String(), " ")
}

// IsTTY reports whether f is an interactive terminal (honest TTY detection).
func IsTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

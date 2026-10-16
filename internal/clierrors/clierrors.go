// Package clierrors defines the stable, agent-facing error model.
//
// Every error carries a Code (mapped to a stable process exit code), a
// human message, and optionally a Hint ("what to do next") and Valid
// ("accepted values"). All three travel through --json envelopes, so both
// humans and agents learn from the same failure.
package clierrors

import (
	"fmt"
	"strings"
)

type Code string

const (
	CodeInternal Code = "internal"
	CodeUsage    Code = "usage"    // bad args/flags/values; not retryable
	CodeConfirm  Code = "confirm"  // destructive op not confirmed; pass --yes
	CodeDevice   Code = "device"   // bulb/transport/config I/O failure
	CodeProtocol Code = "protocol" // bulb spoke something the CLI can't parse
	CodeTimeout  Code = "timeout"  // retry with a larger --timeout
	CodeConflict Code = "conflict" // target already exists / state mismatch
)

var exitCodes = map[Code]int{
	CodeInternal: 1,
	CodeUsage:    2,
	CodeConfirm:  3,
	CodeDevice:   4,
	CodeProtocol: 5,
	CodeTimeout:  6,
	CodeConflict: 7,
}

type Error struct {
	Code    Code
	Message string
	Hint    string
	Valid   []string
	Cause   error
}

func New(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func (e *Error) WithHint(format string, args ...any) *Error {
	e.Hint = fmt.Sprintf(format, args...)
	return e
}

func (e *Error) WithValid(values ...string) *Error {
	e.Valid = values
	return e
}

func (e *Error) WithCause(cause error) *Error {
	e.Cause = cause
	return e
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

// Lines renders the error for stderr: "error: <message>" plus optional
// hint/valid lines. Used in human mode and as the basis of the JSON model.
func (e *Error) Lines() []string {
	lines := []string{"error: " + e.Error()}
	if e.Hint != "" {
		lines = append(lines, "hint: "+e.Hint)
	}
	if len(e.Valid) > 0 {
		lines = append(lines, "valid: "+strings.Join(e.Valid, ", "))
	}
	return lines
}

func (e *Error) ExitCode() int { return exitCodes[e.Code] }

// All returns a copy of the code → exit-code map for schema/agentcontext.
func All() map[Code]int {
	out := make(map[Code]int, len(exitCodes))
	for c, v := range exitCodes {
		out[c] = v
	}
	return out
}

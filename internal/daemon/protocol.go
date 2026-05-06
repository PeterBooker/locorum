package daemon

import (
	"encoding/json"
	"errors"
)

// Wire-format constants. Locorum's IPC speaks JSON-RPC 2.0 framed by
// newlines (no Content-Length headers — that's an LSP convention, not
// a JSON-RPC requirement). One request per line, one response per line.
// Notifications (method calls without "id") are not used today; the
// dispatcher accepts them silently for forward-compat.
const (
	jsonRPCVersion = "2.0"

	// MaxMessageBytes caps an inbound frame at 8 MiB. Generous enough
	// for any realistic Locorum payload (a 5 MiB log tail is on the
	// upper bound of what we render in the GUI), and small enough that
	// a hostile or buggy client can't exhaust the daemon's heap with
	// unbounded JSON.
	MaxMessageBytes = 8 * 1024 * 1024
)

// JSON-RPC error codes. Values < -32000 are reserved by the spec for
// transport-layer errors; method-specific errors live in the
// implementation-defined range -32000..-32099.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603

	// CodeNotFound is returned when a method's target (site, snapshot,
	// container) does not exist. Mapped from generic "not found"
	// errors at the dispatcher boundary so clients can branch on it.
	CodeNotFound = -32001

	// CodeForbidden is returned when the caller's profile / scope does
	// not permit the requested method. Distinguishes a security refusal
	// from a buggy client.
	CodeForbidden = -32002

	// CodeAborted indicates the daemon was shutting down or otherwise
	// could not service the request right now. Clients may retry.
	CodeAborted = -32003

	// CodeConflict reports that the request collides with current
	// state — e.g. deleting a worktree with uncommitted changes
	// without --force. The error message includes the actionable
	// remediation; clients usually surface it directly.
	CodeConflict = -32004
)

// Request is the inbound JSON-RPC frame.
//
// Params is left as raw bytes so per-method handlers can unmarshal into
// their own typed struct without paying a re-marshal at the dispatcher.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is the outbound JSON-RPC frame. Exactly one of Result or
// Error is set.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError matches the JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// ErrNoDaemon is returned by Dial when no daemon is running. CLI clients
// catch this to decide whether to auto-spawn.
var ErrNoDaemon = errors.New("no locorum daemon running")

// MethodError builds a typed RPCError for handler returns. The
// dispatcher unwraps these into the response's Error field and leaves
// untyped errors to map to CodeInternalError.
type MethodError struct {
	Code    int
	Message string
	Cause   error
}

func (e *MethodError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *MethodError) Unwrap() error { return e.Cause }

// NewMethodError constructs a MethodError with a typed code.
func NewMethodError(code int, msg string, cause error) *MethodError {
	return &MethodError{Code: code, Message: msg, Cause: cause}
}

// NotFound is a sugar constructor for the common case.
func NotFound(what string) *MethodError {
	return &MethodError{Code: CodeNotFound, Message: what + " not found"}
}

// Forbidden is the profile-rejection sugar constructor.
func Forbidden(method string) *MethodError {
	return &MethodError{Code: CodeForbidden, Message: "method not permitted: " + method}
}

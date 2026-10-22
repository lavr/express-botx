package botx

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Sentinel errors for errors.Is. An *APIError matches the ones that describe it;
// several can match at once, e.g. a 200 response with reason "admins_not_changed"
// matches both ErrAdminsNotChanged and ErrInvalidResponse.
var (
	// ErrUnauthorized: HTTP 401, the token or the bot secret was rejected.
	ErrUnauthorized = errors.New("unauthorized (HTTP 401)")
	// ErrNotFound: HTTP 404 or a reason ending in "_not_found" (chat_not_found, user_not_found, ...).
	ErrNotFound = errors.New("not found")
	// ErrNoPermission: reason "no_permission_for_operation", e.g. the bot is not a chat admin.
	ErrNoPermission = errors.New("no permission for operation")
	// ErrAdminsNotChanged: reason "admins_not_changed", every user given to AddAdmins already is an admin.
	ErrAdminsNotChanged = errors.New("admins not changed")
	// ErrInvalidResponse: a 2xx response that cannot be trusted: status other than "ok",
	// a body that is not JSON, or a result with required fields missing.
	ErrInvalidResponse = errors.New("invalid response")
)

// APIError is a BotX API response that is not a success.
type APIError struct {
	// Op names the failed operation, e.g. "chat info".
	Op string
	// StatusCode is the HTTP status of the response.
	StatusCode int
	// Status is the "status" field of the response body, if present.
	Status string
	// Reason is the "reason" field of the response body, if present.
	Reason string
	// Body is the response body, cut to a few kilobytes.
	Body string

	msg   string
	cause error
}

func (e *APIError) Error() string { return e.msg }

// Unwrap returns the error that broke reading the response, if any.
func (e *APIError) Unwrap() error { return e.cause }

// Is reports whether e matches one of the sentinel errors of this package.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.StatusCode == 401
	case ErrNotFound:
		return e.StatusCode == 404 || strings.HasSuffix(e.Reason, "_not_found")
	case ErrNoPermission:
		return e.Reason == "no_permission_for_operation"
	case ErrAdminsNotChanged:
		return e.Reason == "admins_not_changed"
	case ErrInvalidResponse:
		return e.StatusCode >= 200 && e.StatusCode <= 299
	}
	return false
}

func newAPIError(op string, code int, body []byte, format string, args ...any) *APIError {
	e := &APIError{Op: op, StatusCode: code, Body: truncateBody(body), msg: fmt.Sprintf(format, args...)}
	e.Status, e.Reason = envelopeOf(body)
	return e
}

func unauthorized(op string, body []byte) *APIError {
	return newAPIError(op, 401, body, "%s", ErrUnauthorized.Error())
}

const maxErrorBodyBytes = 2 << 10

func truncateBody(data []byte) string {
	s, _ := truncateErrorBody(data)
	return s
}

// truncateErrorBody cuts a body for an error message or a log line on a rune boundary.
func truncateErrorBody(data []byte) (string, bool) {
	if len(data) <= maxErrorBodyBytes {
		return string(data), false
	}
	cut := maxErrorBodyBytes
	for cut > 0 && !utf8.RuneStart(data[cut]) {
		cut--
	}
	return fmt.Sprintf("%s (truncated to %d of %d bytes)", data[:cut], cut, len(data)), true
}

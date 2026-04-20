package confluence

import (
	"errors"
	"fmt"
)

// Sentinel errors allow callers to check for specific classes of failure
// with errors.Is rather than string-matching.
var (
	ErrAuth     = errors.New("authentication failed")
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("version conflict")
	ErrAPI      = errors.New("API error")
)

// APIError carries the HTTP status and response body for diagnostic purposes.
type APIError struct {
	Status int
	URL    string
	Body   string
	Class  error
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: %s %d: %s", e.Class, e.URL, e.Status, truncate(e.Body, 400))
}

func (e *APIError) Unwrap() error { return e.Class }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

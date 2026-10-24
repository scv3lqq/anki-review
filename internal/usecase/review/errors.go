package review

import "errors"

// Conflict codes tell the page what to do next.
const (
	// CodeSession: the tab shows another pass, reload it.
	CodeSession = "session"
	// CodeSent: this stage was already posted.
	CodeSent = "sent"
	// CodeEmpty: nothing left to review.
	CodeEmpty = "empty"
	// CodeBusy: a pass or a stage is still running.
	CodeBusy = "busy"
)

// ConflictError is a request that doesn't fit the stage of the pass. Messages are shown on the page.
type ConflictError struct {
	Code    string
	Message string
}

func (e *ConflictError) Error() string { return e.Message }

// GradingFailedError carries the reason Claude didn't grade the pass; the page offers a retry.
type GradingFailedError struct {
	Reason string
}

func (e *GradingFailedError) Error() string { return "grading failed: " + e.Reason }

var (
	// ErrUnknownDeck is a deck name Anki doesn't have.
	ErrUnknownDeck = errors.New("no such deck")
	// ErrNotReady means the grades are still being made.
	ErrNotReady = errors.New("results not ready")
)

func conflict(code, msg string) error { return &ConflictError{Code: code, Message: msg} }

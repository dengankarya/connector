package common

import (
	"errors"
	"net/http"
	"strings"
)

// DomainError is a structured application error carrying a machine-readable code
// and a human-readable message. The HTTP status is derived from the code prefix:
//
//	BR_ → 400 Bad Request
//	AU_ → 401 Unauthorized
//	PR_ → 402 Payment Required
//	FB_ → 403 Forbidden
//	NF_ → 404 Not Found
//	CF_ → 409 Conflict
//	IN_ → 500 Internal Server Error
type DomainError struct {
	Code    string
	Message string
}

func (e *DomainError) Error() string { return e.Message }

// Is enables errors.Is comparison by code, so sentinel vars work correctly with wrapping.
func (e *DomainError) Is(target error) bool {
	var t *DomainError
	if errors.As(target, &t) {
		return e.Code == t.Code
	}
	return false
}

// HTTPStatus returns the HTTP status code derived from the error code prefix.
func (e *DomainError) HTTPStatus() int {
	switch {
	case strings.HasPrefix(e.Code, "BR_"):
		return http.StatusBadRequest
	case strings.HasPrefix(e.Code, "AU_"):
		return http.StatusUnauthorized
	case strings.HasPrefix(e.Code, "PR_"):
		return http.StatusPaymentRequired
	case strings.HasPrefix(e.Code, "FB_"):
		return http.StatusForbidden
	case strings.HasPrefix(e.Code, "NF_"):
		return http.StatusNotFound
	case strings.HasPrefix(e.Code, "CF_"):
		return http.StatusConflict
	default: // IN_ and any unrecognised prefix
		return http.StatusInternalServerError
	}
}

// NewDomainError creates a DomainError with the given code and message.
func NewDomainError(code, message string) *DomainError {
	return &DomainError{Code: code, Message: message}
}

// ─── Pre-defined shared errors ────────────────────────────────────────────────

var (
	ErrBadRequest          = NewDomainError("BR_BAD_REQUEST", "bad request")
	ErrUnauthorized        = NewDomainError("AU_UNAUTHORIZED", "unauthorized")
	ErrInsufficientBalance = NewDomainError("PR_INSUFFICIENT_BALANCE", "insufficient balance")
	ErrForbidden           = NewDomainError("FB_FORBIDDEN", "forbidden")
	ErrNotFound            = NewDomainError("NF_NOT_FOUND", "not found")
	ErrConflict            = NewDomainError("CF_CONFLICT", "conflict")
	ErrInternal            = NewDomainError("IN_INTERNAL_ERROR", "internal server error")
)

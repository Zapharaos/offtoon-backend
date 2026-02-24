package supervisor

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Error struct {
	Id        uuid.UUID `json:"id"`
	Scope     string    `json:"scope"`
	Message   string    `json:"message"`
	Severity  string    `json:"severity"`
	CreatedAt time.Time `json:"createdAt"`
}

// IsValid checks if the error is valid
func (e Error) IsValid() (bool, error) {
	if e.Scope == "" {
		return false, errors.New("scope is empty")
	}

	if e.Message == "" {
		return false, errors.New("message is empty")
	}

	if e.Severity == "" {
		return false, errors.New("severity is empty")
	}

	return true, nil
}

// NewWarningError creates a new warning error
func NewWarningError(scope, message string) Error {
	return Error{
		Scope:    scope,
		Message:  message,
		Severity: "warning",
	}
}

// NewError creates a new error
func NewError(scope, message string) Error {
	return Error{
		Scope:    scope,
		Message:  message,
		Severity: "error",
	}
}

// NewCriticalError creates a new critical error
func NewCriticalError(scope, message string) Error {
	return Error{
		Scope:    scope,
		Message:  message,
		Severity: "critical",
	}
}

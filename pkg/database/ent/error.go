package ent

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
)

// ErrorPredicates lets an application register recognizers for its generated
// Ent error types (e.g. generate.IsNotFound), since each Ent codebase generates
// its own error types that this shared package cannot import.
type ErrorPredicates struct {
	IsNotFound        func(error) bool
	IsValidationError func(error) bool
	IsConstraintError func(error) bool
	IsNotLoaded       func(error) bool
	IsNotSingular     func(error) bool
}

// registered holds application-provided predicates. Register at startup,
// before serving traffic — reads are not synchronized.
var registered []ErrorPredicates

// RegisterErrorPredicates registers generated-Ent error recognizers.
// Call once at application startup.
func RegisterErrorPredicates(p ErrorPredicates) {
	registered = append(registered, p)
}

// NotFoundError returns when trying to fetch a specific entity and it was not found in the database.
type NotFoundError struct {
	label string
}

// Error implements the error interface.
func (e *NotFoundError) Error() string {
	return "ent: " + e.label + " not found"
}

// IsNotFound returns a boolean indicating whether the error is a not found error.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var e *NotFoundError
	return errors.As(err, &e)
}

// ValidationError returns when validating a field or edge fails.
type ValidationError struct {
	Name string // Field or edge name.
	err  error
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	return e.err.Error()
}

// Unwrap implements the errors.Wrapper interface.
func (e *ValidationError) Unwrap() error {
	return e.err
}

// IsValidationError returns a boolean indicating whether the error is a validation error.
func IsValidationError(err error) bool {
	if err == nil {
		return false
	}
	var e *ValidationError
	return errors.As(err, &e)
}

// ConstraintError returns when trying to create/update one or more entities and
// one or more of their constraints failed. For example, violation of edge or
// field uniqueness.
type ConstraintError struct {
	msg  string
	wrap error
}

// Error implements the error interface.
func (e ConstraintError) Error() string {
	return "ent: constraint failed: " + e.msg
}

// Unwrap implements the errors.Wrapper interface.
func (e *ConstraintError) Unwrap() error {
	return e.wrap
}

// IsConstraintError returns a boolean indicating whether the error is a constraint failure.
func IsConstraintError(err error) bool {
	if err == nil {
		return false
	}
	var e *ConstraintError
	return errors.As(err, &e)
}

// NotLoadedError returns when trying to get a node that was not loaded by the query.
type NotLoadedError struct {
	edge string
}

// Error implements the error interface.
func (e *NotLoadedError) Error() string {
	return "ent: " + e.edge + " edge was not loaded"
}

// IsNotLoaded returns a boolean indicating whether the error is a not loaded error.
func IsNotLoaded(err error) bool {
	if err == nil {
		return false
	}
	var e *NotLoadedError
	return errors.As(err, &e)
}

// NotSingularError returns when trying to fetch a singular entity and more then one was found in the database.
type NotSingularError struct {
	label string
}

// Error implements the error interface.
func (e *NotSingularError) Error() string {
	return "ent: " + e.label + " not singular"
}

// IsNotSingular returns a boolean indicating whether the error is a not singular error.
func IsNotSingular(err error) bool {
	if err == nil {
		return false
	}
	var e *NotSingularError
	return errors.As(err, &e)
}

// isNotFound checks both custom and registered generated Ent NotFoundError.
func isNotFound(err error) bool {
	if IsNotFound(err) {
		return true
	}
	for _, p := range registered {
		if p.IsNotFound != nil && p.IsNotFound(err) {
			return true
		}
	}
	return false
}

// isValidationError checks both custom and registered generated Ent ValidationError.
func isValidationError(err error) bool {
	if IsValidationError(err) {
		return true
	}
	for _, p := range registered {
		if p.IsValidationError != nil && p.IsValidationError(err) {
			return true
		}
	}
	return false
}

// isConstraintError checks both custom and registered generated Ent ConstraintError.
func isConstraintError(err error) bool {
	if IsConstraintError(err) {
		return true
	}
	for _, p := range registered {
		if p.IsConstraintError != nil && p.IsConstraintError(err) {
			return true
		}
	}
	return false
}

// isNotLoaded checks both custom and registered generated Ent NotLoadedError.
func isNotLoaded(err error) bool {
	if IsNotLoaded(err) {
		return true
	}
	for _, p := range registered {
		if p.IsNotLoaded != nil && p.IsNotLoaded(err) {
			return true
		}
	}
	return false
}

// isNotSingular checks both custom and registered generated Ent NotSingularError.
func isNotSingular(err error) bool {
	if IsNotSingular(err) {
		return true
	}
	for _, p := range registered {
		if p.IsNotSingular != nil && p.IsNotSingular(err) {
			return true
		}
	}
	return false
}

// MapEntError maps Ent errors to apperr.AppError
func MapEntError(err error, messagePrefix string) *apperr.AppError {
	if err == nil {
		return nil
	}

	if isNotFound(err) {
		return apperr.New(response.CodeNotFound, fmt.Sprintf("%s not found", messagePrefix), err)
	}

	if isValidationError(err) {
		return apperr.New(response.CodeValidationFailed, fmt.Sprintf("%s validation failed", messagePrefix), err)
	}

	if isConstraintError(err) {
		errStr := strings.ToLower(err.Error())

		switch {
		case strings.Contains(errStr, "duplicate") || strings.Contains(errStr, "unique"):
			return apperr.New(response.CodeConflict, fmt.Sprintf("%s already exists", messagePrefix), err)

		case strings.Contains(errStr, "foreign key"):
			if strings.Contains(errStr, "delete") || strings.Contains(errStr, "update") {
				return apperr.New(response.CodeConflict, fmt.Sprintf("%s cannot be modified because it is referenced by other records", messagePrefix), err)
			}
			return apperr.New(response.CodeBadRequest, fmt.Sprintf("%s contains invalid reference data", messagePrefix), err)

		case strings.Contains(errStr, "deadlock"):
			slog.Error("Database deadlock occurred", "error", err)
			return apperr.New(response.CodeDatabaseError, "Operation temporarily unavailable, please try again", err)
		}

		return apperr.New(response.CodeConflict, fmt.Sprintf("%s constraint failed", messagePrefix), err)
	}

	if isNotLoaded(err) {
		slog.Error("Server logic error: edge was not loaded before access", "error", err)
		return apperr.New(response.CodeInternalServer, "Internal server error", err)
	}

	if isNotSingular(err) {
		return apperr.New(response.CodeInternalError, fmt.Sprintf("%s is not uniquely identifiable", messagePrefix), err)
	}

	slog.Error("Unexpected database error", "error", err)
	return apperr.New(response.CodeDatabaseError, "An unexpected database error occurred", err)
}

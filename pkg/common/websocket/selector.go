package websocket

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	CloseNormal             = 1000
	CloseGoingAway          = 1001
	CloseProtocolError      = 1002
	CloseUnsupportedData    = 1003
	CloseInvalidPayload     = 1007
	ClosePolicyViolation    = 1008
	CloseMessageTooBig      = 1009
	CloseInternalError      = 1011
	CloseServiceRestart     = 1012
	CloseTryAgainLater      = 1013
	CloseBadGateway         = 1014
	CloseAuthenticationGone = 4001

	maxSelectorValues   = 128
	maxSelectorBytes    = 256
	maxCloseReasonBytes = 123
)

type PrincipalClass uint8

const (
	PrincipalAny PrincipalClass = iota
	PrincipalAuthenticated
	PrincipalAnonymous
)

type ConnectionSelector struct {
	All            bool
	PrincipalClass PrincipalClass
	Subjects       []string
	SessionIDs     []string
	ConnectionIDs  []string
	Topics         []string
}

type CloseOptions struct {
	Code   int
	Reason string
}

type CloseResult struct {
	Matched        int
	MarkedClosing  int
	AlreadyClosing int
}

func DefaultCloseOptions() CloseOptions {
	return CloseOptions{Code: CloseNormal, Reason: "normal closure"}
}

func validateSelector(selector ConnectionSelector) error {
	if selector.PrincipalClass != PrincipalAny &&
		selector.PrincipalClass != PrincipalAuthenticated &&
		selector.PrincipalClass != PrincipalAnonymous {
		return ErrInvalidSelector
	}
	if !selector.All &&
		len(selector.Subjects) == 0 &&
		len(selector.SessionIDs) == 0 &&
		len(selector.ConnectionIDs) == 0 &&
		len(selector.Topics) == 0 {
		return ErrInvalidSelector
	}
	if selector.PrincipalClass == PrincipalAnonymous &&
		(len(selector.Subjects) > 0 || len(selector.SessionIDs) > 0) {
		return ErrInvalidSelector
	}
	for _, values := range [][]string{
		selector.Subjects,
		selector.SessionIDs,
		selector.ConnectionIDs,
		selector.Topics,
	} {
		if !validSelectorValues(values) {
			return ErrInvalidSelector
		}
	}
	return nil
}

func validSelectorValues(values []string) bool {
	if len(values) > maxSelectorValues {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validRequiredTerm(value, maxSelectorBytes) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validateCloseOptions(options CloseOptions) error {
	validCode := false
	switch options.Code {
	case CloseNormal,
		CloseGoingAway,
		CloseProtocolError,
		CloseUnsupportedData,
		CloseInvalidPayload,
		ClosePolicyViolation,
		CloseMessageTooBig,
		CloseInternalError,
		CloseServiceRestart,
		CloseTryAgainLater,
		CloseBadGateway:
		validCode = true
	default:
		validCode = options.Code >= 3000 && options.Code <= 4999
	}
	if !validCode ||
		len(options.Reason) > maxCloseReasonBytes ||
		!utf8.ValidString(options.Reason) ||
		strings.TrimSpace(options.Reason) != options.Reason {
		return ErrInvalidCloseOptions
	}
	for _, character := range options.Reason {
		if unicode.IsControl(character) {
			return ErrInvalidCloseOptions
		}
	}
	return nil
}

func (hub *Hub) CloseConnections(
	ctx context.Context,
	selector ConnectionSelector,
	options CloseOptions,
) (CloseResult, error) {
	if hub == nil || ctx == nil {
		return CloseResult{}, ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return CloseResult{}, err
	}
	if err := validateSelector(selector); err != nil {
		return CloseResult{}, err
	}
	if err := validateCloseOptions(options); err != nil {
		return CloseResult{}, err
	}

	hub.mu.Lock()
	candidates := make(map[string]*Conn, len(hub.state.connections)+len(hub.state.closing))
	for id, connection := range hub.state.connections {
		candidates[id] = connection
	}
	for id, connection := range hub.state.closing {
		candidates[id] = connection
	}
	result := CloseResult{}
	for _, connection := range candidates {
		if !selectorMatches(selector, connection) {
			continue
		}
		result.Matched++
		if connection.state == connectionClosing ||
			connection.state == connectionClosed {
			result.AlreadyClosing++
			continue
		}
		hub.markClosingLocked(connection, options)
		result.MarkedClosing++
	}
	hub.mu.Unlock()
	return result, nil
}

func selectorMatches(selector ConnectionSelector, connection *Conn) bool {
	if connection == nil {
		return false
	}
	switch selector.PrincipalClass {
	case PrincipalAuthenticated:
		if !connection.principal.Authenticated {
			return false
		}
	case PrincipalAnonymous:
		if connection.principal.Authenticated {
			return false
		}
	}
	if len(selector.Subjects) > 0 &&
		!containsString(selector.Subjects, connection.principal.Subject) {
		return false
	}
	if len(selector.SessionIDs) > 0 &&
		!containsString(selector.SessionIDs, connection.principal.SessionID) {
		return false
	}
	if len(selector.ConnectionIDs) > 0 &&
		!containsString(selector.ConnectionIDs, connection.id) {
		return false
	}
	if len(selector.Topics) > 0 {
		matchesTopic := false
		for _, topic := range selector.Topics {
			if _, subscribed := connection.topics[topic]; subscribed {
				matchesTopic = true
				break
			}
		}
		if !matchesTopic {
			return false
		}
	}
	return true
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

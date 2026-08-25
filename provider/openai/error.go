package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxAPIErrorBody = 64 << 10

var (
	// ErrMissingAPIKey indicates that Config contains no usable API key.
	ErrMissingAPIKey = errors.New("openai: missing API key")
	// ErrNoModels indicates that Config contains no model descriptors.
	ErrNoModels = errors.New("openai: no models configured")
	// ErrInvalidModel indicates that a model descriptor is invalid or unavailable.
	ErrInvalidModel = errors.New("openai: invalid model")
	// ErrInvalidRequest indicates that a request cannot be represented by this provider.
	ErrInvalidRequest = errors.New("openai: invalid request")
	// ErrInvalidStream indicates that a streaming response violates the provider protocol.
	ErrInvalidStream = errors.New("openai: invalid stream")
)

// APIError describes a non-success response returned by the OpenAI API.
type APIError struct {
	// StatusCode is the HTTP response status code.
	StatusCode int
	// RequestID is the OpenAI request identifier, when supplied.
	RequestID string
	// Type classifies the API error.
	Type string
	// Code is the provider-specific error code.
	Code string
	// Param identifies the invalid request parameter, when supplied.
	Param string
	// Message is the provider's bounded error description.
	Message string
}

// Error implements error without exposing credentials or response headers.
func (e *APIError) Error() string {
	if e == nil {
		return "openai: API error"
	}

	detail := e.Message
	if detail == "" {
		detail = http.StatusText(e.StatusCode)
	}
	if detail == "" {
		detail = "request failed"
	}
	if e.Code != "" {
		detail += " (code: " + e.Code + ")"
	}
	return fmt.Sprintf("openai: API error: HTTP %d: %s", e.StatusCode, detail)
}

type apiErrorEnvelope struct {
	Error struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Code    json.RawMessage `json:"code"`
		Param   json.RawMessage `json:"param"`
	} `json:"error"`
}

func readAPIError(response *http.Response) *APIError {
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxAPIErrorBody+1))
	if len(body) > maxAPIErrorBody {
		body = body[:maxAPIErrorBody]
	}

	result := &APIError{
		StatusCode: response.StatusCode,
		RequestID:  response.Header.Get("x-request-id"),
	}
	if readErr != nil {
		result.Message = response.Status
		return result
	}

	var envelope apiErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Message != "" {
		result.Message = envelope.Error.Message
		result.Type = envelope.Error.Type
		result.Code = scalarString(envelope.Error.Code)
		result.Param = scalarString(envelope.Error.Param)
		return result
	}

	result.Message = strings.TrimSpace(string(body))
	if result.Message == "" {
		result.Message = response.Status
	}
	return result
}

func scalarString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return string(raw)
}

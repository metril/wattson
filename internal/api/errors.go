package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// ErrorResponse is the JSON body of every non-2xx response. Handlers must
// not use it as a Go type for responses or errors: return apiErr values,
// and use APIError wherever a type is needed (for example in docs-only
// specs), so the schema is registered once under this name.
type ErrorResponse struct {
	Error ErrorBody `json:"error" doc:"Error details."`
}

// ErrorBody carries a machine-readable code and a human-readable message.
type ErrorBody struct {
	Code    string `json:"code" doc:"Machine-readable error code"`
	Message string `json:"message" doc:"Human-readable explanation"`
}

// APIError is the error typed handlers return: the envelope plus the HTTP
// status. It implements huma.StatusError.
type APIError struct {
	ErrorResponse
	status int
}

// GetStatus implements huma.StatusError.
func (e *APIError) GetStatus() int { return e.status }

// Error implements error.
func (e *APIError) Error() string { return e.ErrorResponse.Error.Message }

// ContentType keeps errors as plain application/json (huma defaults to
// application/problem+json).
func (e *APIError) ContentType(string) string { return "application/json" }

// apiErr builds a typed error response.
func apiErr(status int, code, msg string) error {
	e := &APIError{status: status}
	e.ErrorResponse.Error = ErrorBody{Code: code, Message: msg}
	return e
}

func init() {
	huma.NewError = newHumaError
	// Empty collections are returned as [], so the schema must not allow null.
	huma.DefaultArrayNullable = false
}

// newHumaError maps huma's own failures (validation, malformed body) onto
// the envelope. Validation reports the first problem as "<location>: <msg>".
// 5xx responses never echo the underlying error.
func newHumaError(status int, msg string, errs ...error) huma.StatusError {
	message := msg
	if status < 500 {
		for _, e := range errs {
			if e == nil {
				continue
			}
			var d *huma.ErrorDetail
			if errors.As(e, &d) && d.Location != "" {
				message = d.Location + ": " + d.Message
			} else if errors.As(e, &d) {
				message = d.Message
			} else {
				message = e.Error()
			}
			break
		}
	}
	code := ""
	switch status {
	case http.StatusUnprocessableEntity:
		status, code = http.StatusBadRequest, "invalid_request"
	case http.StatusBadRequest:
		code = "invalid_request"
	case http.StatusUnauthorized:
		code = "unauthorized"
	case http.StatusForbidden:
		code = "forbidden"
	case http.StatusNotFound:
		code = "not_found"
	case http.StatusMethodNotAllowed:
		code = "method_not_allowed"
	case http.StatusConflict:
		code = "conflict"
	case http.StatusRequestEntityTooLarge:
		code = "payload_too_large"
	case http.StatusUnsupportedMediaType:
		code = "unsupported_media_type"
	case http.StatusTooManyRequests:
		code = "rate_limited"
	default:
		if status >= 500 {
			code = "internal_error"
		} else {
			code = strings.ReplaceAll(strings.ToLower(http.StatusText(status)), " ", "_")
		}
	}
	return apiErr(status, code, message).(*APIError)
}

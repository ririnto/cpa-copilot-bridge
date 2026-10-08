package provider

import (
	"fmt"
	"net/http"

	"github.com/tidwall/gjson"
)

type StatusError struct {
	Code       string
	Message    string
	HTTPStatus int
	Retryable  bool
}

func (e *StatusError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func statusError(code, message string, status int) error {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	return &StatusError{Code: code, Message: message, HTTPStatus: status}
}

// upstreamStatusError keeps provider response text out of client-visible errors.
func upstreamStatusError(status int, body string) error {
	code := "upstream_error"
	message := fmt.Sprintf("Copilot upstream returned HTTP %d", status)
	if (status == http.StatusBadRequest || status == http.StatusNotFound || status == http.StatusUnprocessableEntity) &&
		gjson.Valid(body) &&
		gjson.Get(body, "error.code").String() == "model_not_supported" &&
		gjson.Get(body, "error.param").String() == "model" &&
		gjson.Get(body, "error.type").String() == "invalid_request_error" {
		code = "model_not_supported"
		message = "The requested model is not supported."
	}
	if status == http.StatusBadRequest && gjson.Valid(body) &&
		gjson.Get(body, "error.code").String() == "unsupported_value" &&
		gjson.Get(body, "error.message").String() == "The use of the web search tool is not supported." {
		code = "unsupported_value"
		message = `{"error":{"message":"The use of the web search tool is not supported.","code":"unsupported_value","type":"invalid_request_error"}}`
	}
	return &StatusError{
		Code:       code,
		Message:    message,
		HTTPStatus: status,
		Retryable:  status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500,
	}
}

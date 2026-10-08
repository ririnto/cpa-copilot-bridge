package provider

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestUpstreamStatusErrorOmitsResponseText(t *testing.T) {
	detail := "echoed SYNTHETIC_PROMPT_MARKER synthetic/path/file.txt synthetic-token-marker"
	tests := []struct {
		name      string
		status    int
		retryable bool
	}{
		{name: "gateway failure", status: http.StatusBadGateway, retryable: true},
		{name: "unauthorized", status: http.StatusUnauthorized, retryable: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := upstreamStatusError(test.status, detail)
			statusErr, ok := err.(*StatusError)
			if !ok {
				t.Fatal("upstreamStatusError did not return a structured status error")
			}
			if statusErr.Code != "upstream_error" || statusErr.HTTPStatus != test.status || statusErr.Retryable != test.retryable {
				t.Fatal("upstream error metadata changed")
			}
			if statusErr.Message != fmt.Sprintf("Copilot upstream returned HTTP %d", test.status) {
				t.Fatal("upstream error message changed")
			}
			for _, marker := range []string{"SYNTHETIC_PROMPT_MARKER", "synthetic/path/file.txt", "synthetic-token-marker"} {
				if strings.Contains(statusErr.Error(), marker) {
					t.Fatal("upstream error exposed provider response text")
				}
			}
		})
	}
}

func TestUpstreamModelSupportErrorClassification(t *testing.T) {
	body := `{"error":{"message":"echoed SYNTHETIC_PROMPT_MARKER synthetic-token-marker","code":"model_not_supported","param":"model","type":"invalid_request_error"}}`
	for _, test := range []struct {
		name      string
		status    int
		body      string
		wantCode  string
		retryable bool
	}{
		{name: "native model rejection", status: http.StatusBadRequest, body: body, wantCode: "model_not_supported"},
		{name: "model missing", status: http.StatusNotFound, body: body, wantCode: "model_not_supported"},
		{name: "unsupported model", status: http.StatusUnprocessableEntity, body: body, wantCode: "model_not_supported"},
		{name: "authentication stays credential scoped", status: http.StatusUnauthorized, body: body, wantCode: "upstream_error"},
		{name: "server failure stays retryable", status: http.StatusBadGateway, body: body, wantCode: "upstream_error", retryable: true},
		{name: "unstructured provider text", status: http.StatusBadRequest, body: "model_not_supported", wantCode: "upstream_error"},
		{name: "truncated error", status: http.StatusBadRequest, body: `{"error":{"code":"model_not_supported"`, wantCode: "upstream_error"},
		{name: "different request error", status: http.StatusBadRequest, body: `{"error":{"code":"unsupported_value","param":"model"}}`, wantCode: "upstream_error"},
		{name: "different rejected field", status: http.StatusBadRequest, body: `{"error":{"code":"model_not_supported","param":"tools","type":"invalid_request_error"}}`, wantCode: "upstream_error"},
		{name: "different error type", status: http.StatusBadRequest, body: `{"error":{"code":"model_not_supported","param":"model","type":"authentication_error"}}`, wantCode: "upstream_error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := upstreamStatusError(test.status, test.body)
			statusErr, ok := err.(*StatusError)
			if !ok || statusErr.Code != test.wantCode || statusErr.HTTPStatus != test.status || statusErr.Retryable != test.retryable {
				t.Fatalf("unexpected structured error: %#v", statusErr)
			}
			if test.wantCode == "model_not_supported" && statusErr.Message != "The requested model is not supported." {
				t.Fatalf("model rejection lost the CPA-recognized message: %q", statusErr.Message)
			}
			for _, marker := range []string{"SYNTHETIC_PROMPT_MARKER", "synthetic-token-marker"} {
				if strings.Contains(statusErr.Error(), marker) {
					t.Fatal("model rejection exposed provider response text")
				}
			}
		})
	}
}

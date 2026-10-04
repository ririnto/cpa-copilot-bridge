package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ririnto/cpa-copilot-bridge/internal/transport"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type refreshTestHost struct {
	mu                sync.Mutex
	oauthResponse     transport.Response
	oauthCallbackID   string
	oauthRequest      transport.Request
	copilotTokenCalls int
	modelStatuses     []int
	modelRequests     []transport.Request
	streamStatuses    []int
	streamRequests    []transport.Request
	closedStreamIDs   []string
}

func (h *refreshTestHost) Do(ctx context.Context, callbackID string, request transport.Request) (transport.Response, error) {
	if err := ctx.Err(); err != nil {
		return transport.Response{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	request.Body = append([]byte(nil), request.Body...)
	request.Headers = request.Headers.Clone()
	if strings.HasSuffix(request.URL, "/login/oauth/access_token") {
		h.oauthCallbackID = callbackID
		h.oauthRequest = request
		return h.oauthResponse, nil
	}
	if strings.HasSuffix(request.URL, "/copilot_internal/v2/token") {
		h.copilotTokenCalls++
		body, err := json.Marshal(map[string]any{"token": fmt.Sprintf("copilot-token-%d", h.copilotTokenCalls), "expires_at": time.Now().Add(time.Hour).Unix()})
		if err != nil {
			return transport.Response{}, err
		}
		return transport.Response{StatusCode: http.StatusOK, Body: body}, nil
	}
	h.modelRequests = append(h.modelRequests, request)
	status := nextStatus(&h.modelStatuses)
	if status == http.StatusOK && strings.HasSuffix(request.URL, "/models") {
		return transport.Response{StatusCode: status, Body: []byte(`{"data":[{"id":"model-a"}]}`)}, nil
	}
	if status == http.StatusOK {
		return transport.Response{StatusCode: status, Body: []byte(`{"ok":true}`)}, nil
	}
	return transport.Response{StatusCode: status, Body: []byte(`{"error":"unauthorized"}`)}, nil
}

func (h *refreshTestHost) OpenStream(ctx context.Context, _ string, request transport.Request) (transport.Stream, error) {
	if err := ctx.Err(); err != nil {
		return transport.Stream{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	request.Body = append([]byte(nil), request.Body...)
	request.Headers = request.Headers.Clone()
	h.streamRequests = append(h.streamRequests, request)
	status := nextStatus(&h.streamStatuses)
	return transport.Stream{ID: fmt.Sprintf("stream-%d", len(h.streamRequests)), StatusCode: status}, nil
}

func (h *refreshTestHost) ReadStream(context.Context, string) (transport.StreamChunk, error) {
	return transport.StreamChunk{Done: true}, nil
}

func (h *refreshTestHost) CloseStream(_ context.Context, streamID string) error {
	h.mu.Lock()
	h.closedStreamIDs = append(h.closedStreamIDs, streamID)
	h.mu.Unlock()
	return nil
}

func (h *refreshTestHost) Emit(context.Context, string, []byte) error { return nil }

func (h *refreshTestHost) CloseOutput(context.Context, string, string) {}

func nextStatus(statuses *[]int) int {
	if len(*statuses) == 0 {
		return http.StatusOK
	}
	status := (*statuses)[0]
	*statuses = (*statuses)[1:]
	return status
}

func TestRefreshAuthReturnsRotatedGitHubAuthData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	responseBody, err := json.Marshal(oauthTokenResponse{AccessToken: "rotated-access", RefreshToken: "rotated-refresh", TokenType: "bearer", Scope: "read:user user:email", ExpiresIn: 3600, RefreshTokenExpiresIn: 86400})
	if err != nil {
		t.Fatalf("marshal OAuth response: %v", err)
	}
	host := &refreshTestHost{oauthResponse: transport.Response{StatusCode: http.StatusOK, Body: responseBody}}
	service := New(host)
	service.now = func() time.Time { return now }
	storage := authStorage{Type: providerID, GitHubAccessToken: "old-access", GitHubRefreshToken: "old-refresh", GitHubLogin: "fixture-user", OAuthClientID: "stored-client", ExpiresAt: now.Add(-time.Minute).Unix(), RefreshTokenExpiresAt: now.Add(24 * time.Hour).Unix()}
	rawStorage, err := marshalStorage(storage)
	if err != nil {
		t.Fatalf("marshal auth storage: %v", err)
	}
	result, err := service.RefreshAuth(context.Background(), "host-callback", pluginapi.AuthRefreshRequest{AuthID: "auth-id", StorageJSON: rawStorage})
	if err != nil {
		t.Fatalf("RefreshAuth: %v", err)
	}
	var refreshed authStorage
	if err := json.Unmarshal(result.Auth.StorageJSON, &refreshed); err != nil {
		t.Fatalf("decode refreshed storage: %v", err)
	}
	if refreshed.GitHubAccessToken != "rotated-access" || refreshed.GitHubRefreshToken != "rotated-refresh" || refreshed.TokenType != "bearer" || refreshed.Scope != "read:user user:email" {
		t.Fatalf("rotated auth fields not returned: %#v", refreshed)
	}
	wantAccessExpiry := now.Add(time.Hour).Unix()
	wantRefreshExpiry := now.Add(24 * time.Hour).Unix()
	if refreshed.ExpiresAt != wantAccessExpiry || refreshed.RefreshTokenExpiresAt != wantRefreshExpiry || refreshed.UpdatedAt != now.Format(time.RFC3339) {
		t.Fatalf("refreshed expiry or update time = %#v", refreshed)
	}
	wantNextRefresh := time.Unix(wantAccessExpiry, 0).Add(-10 * time.Minute)
	if !result.NextRefreshAfter.Equal(wantNextRefresh) || !result.Auth.NextRefreshAfter.Equal(wantNextRefresh) {
		t.Fatalf("next refresh = %s and auth next refresh = %s, want %s", result.NextRefreshAfter, result.Auth.NextRefreshAfter, wantNextRefresh)
	}
	host.mu.Lock()
	callbackID, request := host.oauthCallbackID, host.oauthRequest
	host.mu.Unlock()
	if callbackID != "host-callback" || request.URL != "https://github.com/login/oauth/access_token" || request.Method != http.MethodPost {
		t.Fatalf("OAuth refresh callback/request = %q %+v", callbackID, request)
	}
	form, err := url.ParseQuery(string(request.Body))
	if err != nil || form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "old-refresh" || form.Get("client_id") != "stored-client" {
		t.Fatalf("OAuth refresh request form was incorrect: %v", err)
	}
}

func TestRefreshAuthInvalidGrantIsSafeAndClassifiable(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			host := &refreshTestHost{oauthResponse: transport.Response{StatusCode: status, Body: []byte(`{"error":"invalid_grant","error_description":"refresh secret rejected"}`)}}
			service := New(host)
			service.now = func() time.Time { return now }
			storage := authStorage{Type: providerID, GitHubAccessToken: "access-secret", GitHubRefreshToken: "refresh-secret", GitHubLogin: "fixture-user", ExpiresAt: now.Add(-time.Minute).Unix()}
			rawStorage, err := marshalStorage(storage)
			if err != nil {
				t.Fatalf("marshal auth storage: %v", err)
			}
			_, err = service.RefreshAuth(context.Background(), "callback", pluginapi.AuthRefreshRequest{AuthID: "auth-id", StorageJSON: rawStorage})
			statusErr, ok := err.(*StatusError)
			if !ok || statusErr.Code != "invalid_grant" || statusErr.Message != "invalid_grant" || statusErr.HTTPStatus != status {
				t.Fatalf("refresh error = %#v, want safe invalid_grant status %d", err, status)
			}
			if strings.Contains(err.Error(), "refresh secret") || strings.Contains(err.Error(), "access-secret") || strings.Contains(err.Error(), "refresh-secret") {
				t.Fatalf("refresh error leaked credentials or response description: %v", err)
			}
		})
	}
}

func TestModelEndpoint401RetriesAreBounded(t *testing.T) {
	for _, test := range []struct {
		name               string
		operation          string
		statuses           []int
		wantStatus         int
		wantModelCalls     int
		wantTokenExchanges int
	}{
		{name: "nonstream recovers once", operation: "nonstream", statuses: []int{http.StatusUnauthorized, http.StatusOK}, wantStatus: http.StatusOK, wantModelCalls: 2, wantTokenExchanges: 1},
		{name: "nonstream stops after one retry", operation: "nonstream", statuses: []int{http.StatusUnauthorized, http.StatusUnauthorized, http.StatusOK}, wantStatus: http.StatusUnauthorized, wantModelCalls: 2, wantTokenExchanges: 1},
		{name: "stream recovers once", operation: "stream", statuses: []int{http.StatusUnauthorized, http.StatusOK}, wantStatus: http.StatusOK, wantModelCalls: 2, wantTokenExchanges: 1},
		{name: "stream stops after one retry", operation: "stream", statuses: []int{http.StatusUnauthorized, http.StatusUnauthorized, http.StatusOK}, wantStatus: http.StatusUnauthorized, wantModelCalls: 2, wantTokenExchanges: 1},
		{name: "models recovers once", operation: "models", statuses: []int{http.StatusUnauthorized, http.StatusOK}, wantStatus: http.StatusOK, wantModelCalls: 2, wantTokenExchanges: 2},
		{name: "models stops after one retry", operation: "models", statuses: []int{http.StatusUnauthorized, http.StatusUnauthorized, http.StatusOK}, wantStatus: http.StatusUnauthorized, wantModelCalls: 2, wantTokenExchanges: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := &refreshTestHost{}
			service := New(host)
			storage := authStorage{GitHubAccessToken: "github-access"}
			token := copilotTokenEntry{Token: "copilot-initial", APIBaseURL: "https://api.example", ExpiresAt: time.Now().Add(time.Hour), Fingerprint: tokenFingerprint(storage.GitHubAccessToken)}
			var gotStatus int
			var err error
			if test.operation == "stream" {
				host.streamStatuses = append([]int(nil), test.statuses...)
				stream, _, errOpen := service.openModelStream(context.Background(), "callback", "auth-id", storage, token, "/responses", []byte(`{}`))
				err = errOpen
				gotStatus = stream.StatusCode
			} else {
				host.modelStatuses = append([]int(nil), test.statuses...)
				if test.operation == "models" {
					models, _, errModels := service.models(context.Background(), "callback", "auth-id", storage, true)
					err = errModels
					if err == nil {
						gotStatus = http.StatusOK
						if len(models) == 0 {
							t.Fatal("model discovery returned no models")
						}
					}
				} else {
					response, _, errRequest := service.doModelRequest(context.Background(), "callback", "auth-id", storage, token, "/responses", []byte(`{}`), false)
					err = errRequest
					gotStatus = response.StatusCode
				}
			}
			if err != nil {
				statusErr, ok := err.(*StatusError)
				if !ok || statusErr.HTTPStatus != test.wantStatus {
					t.Fatalf("request error = %#v, want status %d", err, test.wantStatus)
				}
				gotStatus = statusErr.HTTPStatus
			}
			if gotStatus != test.wantStatus {
				t.Fatalf("final upstream status = %d, want %d", gotStatus, test.wantStatus)
			}
			host.mu.Lock()
			modelCalls, tokenCalls := len(host.modelRequests), host.copilotTokenCalls
			streamCalls := len(host.streamRequests)
			modelRequests := append([]transport.Request(nil), host.modelRequests...)
			streamRequests := append([]transport.Request(nil), host.streamRequests...)
			closedStreamIDs := append([]string(nil), host.closedStreamIDs...)
			host.mu.Unlock()
			if modelCalls+streamCalls != test.wantModelCalls || tokenCalls != test.wantTokenExchanges {
				t.Fatalf("upstream calls = model:%d stream:%d tokens:%d, want model+stream:%d tokens:%d", modelCalls, streamCalls, tokenCalls, test.wantModelCalls, test.wantTokenExchanges)
			}
			firstToken, secondToken := "copilot-initial", "copilot-token-1"
			if test.operation == "models" {
				firstToken, secondToken = "copilot-token-1", "copilot-token-2"
			}
			requests := modelRequests
			if test.operation == "stream" {
				requests = streamRequests
				if len(closedStreamIDs) != 1 || closedStreamIDs[0] != "stream-1" {
					t.Fatalf("closed unauthorized streams = %v, want [stream-1]", closedStreamIDs)
				}
			}
			if len(requests) != 2 || requests[0].Headers.Get("Authorization") != "Bearer "+firstToken || requests[1].Headers.Get("Authorization") != "Bearer "+secondToken {
				t.Fatalf("retry authorization headers = %v, want old then refreshed token", requests)
			}
		})
	}
}

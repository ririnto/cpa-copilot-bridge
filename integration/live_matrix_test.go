package integration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arthur-sommer-etc/cliproxyapi-copilot-plugin/internal/provider"
	"github.com/arthur-sommer-etc/cliproxyapi-copilot-plugin/internal/transport"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

const liveCopilotClientKey = "fixture-client-key"

var liveCopilotRoutes = map[string]string{
	"gemini-3.8-flash": "/chat/completions",
	"gpt-6-luna":       "/responses",
	"claude-haiku-5.5": "/v1/messages",
}

type liveCatalogModel struct {
	ID                 string   `json:"id"`
	SupportedEndpoints []string `json:"supported_endpoints"`
}

type liveCatalogResponse struct {
	Data []liveCatalogModel `json:"data"`
}

type liveProfile struct {
	AuthMode    string
	StorageJSON []byte
	Catalog     map[string][]string
}

type liveCatalogHost struct {
	client *http.Client
	mu     sync.Mutex
	body   []byte
}

func TestLiveCopilotProtocolMatrix(t *testing.T) {
	if os.Getenv("CPA_LIVE_COPILOT_MATRIX") != "1" {
		t.Skip("set CPA_LIVE_COPILOT_MATRIX=1 to run real Copilot requests")
	}
	binary := os.Getenv("CPA_BINARY")
	if binary == "" {
		t.Fatal("CPA_BINARY is required for the live native host matrix")
	}
	token, err := readCopilotCLIKeychainToken()
	if err != nil {
		t.Fatal("could not read the Copilot CLI credential")
	}
	login, userID, err := liveGitHubIdentity(token)
	if err != nil {
		t.Fatal("could not resolve authenticated GitHub identity")
	}
	storageJSON, err := liveCopilotStorage(token, login, userID)
	if err != nil {
		t.Fatal("could not prepare in-memory live auth state")
	}
	profile := discoverLiveProfile(t, storageJSON)
	overrides := liveEndpointOverrides(profile.Catalog)
	for model, endpoint := range liveCopilotRoutes {
		advertised := profile.Catalog[model]
		t.Logf("model=%s advertised_endpoints=%v selected_endpoint=%s selected_by=native endpoint override", model, advertised, endpoint)
	}
	base, stop := startLiveNativeHost(t, binary, profile.StorageJSON, profile.AuthMode, overrides)
	defer stop()

	valid := 0
	attempted := 0
	for _, model := range []string{"gemini-3.8-flash", "gpt-6-luna", "claude-haiku-5.5"} {
		for _, api := range []string{"Chat", "Responses", "Claude Messages"} {
			attempted++
			name := fmt.Sprintf("%s/%s/stream=false", model, strings.ReplaceAll(api, " ", "_"))
			if !t.Run(name, func(t *testing.T) {
				if runLiveMatrixCell(t, base, model, api, false) {
					valid++
				}
			}) {
				continue
			}
			attempted++
			streamName := fmt.Sprintf("%s/%s/stream=true", model, strings.ReplaceAll(api, " ", "_"))
			if t.Run(streamName, func(t *testing.T) {
				if runLiveMatrixCell(t, base, model, api, true) {
					valid++
				}
			}) {
				continue
			}
		}
	}
	t.Logf("live matrix requests attempted=%d valid=%d", attempted, valid)
	t.Run("UnsupportedModel", func(t *testing.T) {
		body, status, _, err := liveProxyCall(base+"/v1/responses", map[string]any{
			"model": "cpa-live-model-not-in-catalog-7f8c",
			"input": "Reply with one word.",
		})
		if err != nil {
			t.Errorf("unsupported-model request failed before receiving a response")
			return
		}
		if status < 400 || status >= 500 || safeLiveErrorCode(body) != "model_not_found" {
			t.Errorf("unsupported model returned HTTP %d, error_code=%q; expected CPA model_not_found", status, safeLiveErrorCode(body))
		}
	})
	t.Run("UnsupportedCompactionFeature", func(t *testing.T) {
		body, status, _, err := liveProxyCall(base+"/v1/responses/compact", map[string]any{
			"model": "claude-haiku-5.5",
			"input": "Summarize this short request.",
		})
		if err != nil {
			t.Errorf("unsupported-feature request failed before receiving a response")
			return
		}
		if status != http.StatusUnprocessableEntity || safeLiveErrorType(body) != "invalid_request_error" {
			t.Errorf("unsupported compaction returned HTTP %d, error_type=%q; expected CPA's 422 request error", status, safeLiveErrorType(body))
		}
	})
}

func runLiveMatrixCell(t *testing.T, base, model, api string, stream bool) bool {
	t.Helper()
	path, payload := liveMatrixRequest(model, api, stream)
	body, status, contentType, callErr := liveProxyCall(base+path, payload)
	if callErr != nil {
		t.Errorf("request failed before receiving a response")
		return false
	}
	if status != http.StatusOK {
		t.Errorf("request returned HTTP %d, error_class=%q", status, safeLiveErrorClass(body, status))
		return false
	}
	text, returnedModel, schemaOK, terminalOK := validateLiveResponse(api, stream, contentType, body)
	if !schemaOK {
		t.Errorf("response did not match the %s client schema", api)
		return false
	}
	if returnedModel != model {
		t.Errorf("response model=%q, want exact requested model %q", returnedModel, model)
		return false
	}
	if !strings.Contains(strings.ToUpper(text), "LIVE_MATRIX_OK") {
		t.Errorf("assistant text did not include the requested marker")
		return false
	}
	if stream && !terminalOK {
		t.Errorf("stream did not include the protocol terminal event")
		return false
	}
	t.Logf("validated exact model and text response; chars=%d terminal=%t", len([]rune(text)), terminalOK)
	return true
}

func TestLiveResponsesStreamValidation(t *testing.T) {
	const model = "gpt-6-luna"
	const marker = "LIVE_MATRIX_OK"
	completed := `data: {"type":"response.completed","response":{"object":"response","status":"completed","model":"gpt-6-luna","output":[{"type":"message","content":[{"type":"output_text","text":"LIVE_MATRIX_OK"}]}]}}` + "\n\n"
	tests := []struct {
		name         string
		body         string
		wantText     string
		wantModel    string
		wantSchema   bool
		wantTerminal bool
	}{
		{
			name: "delta text wins over completed snapshot",
			body: `data: {"type":"response.created","response":{"object":"response","status":"in_progress","model":"gpt-6-luna"}}` + "\n\n" +
				`data: {"type":"response.output_text.delta","delta":"LIVE_MATRIX_"}` + "\n\n" +
				`data: {"type":"response.output_text.delta","delta":"OK"}` + "\n\n" + completed,
			wantText: marker, wantModel: model, wantSchema: true, wantTerminal: true,
		},
		{
			name:         "completed snapshot supplies missing delta text",
			body:         completed,
			wantText:     marker,
			wantModel:    model,
			wantSchema:   true,
			wantTerminal: true,
		},
		{
			name:         "failed completion is not a valid response schema",
			body:         `data: {"type":"response.completed","response":{"object":"response","status":"failed","model":"gpt-6-luna","output":[]}}` + "\n\n",
			wantModel:    model,
			wantTerminal: true,
		},
		{
			name:         "malformed event invalidates stream",
			body:         completed + "data: {broken json}\n\n",
			wantText:     marker,
			wantModel:    model,
			wantTerminal: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text, returnedModel, schemaOK, terminalOK := validateLiveResponse("Responses", true, "text/event-stream", []byte(test.body))
			if text != test.wantText {
				t.Errorf("stream text=%q, want %q", text, test.wantText)
			}
			if returnedModel != test.wantModel {
				t.Errorf("stream model=%q, want exact requested model %q", returnedModel, test.wantModel)
			}
			if schemaOK != test.wantSchema {
				t.Errorf("schema valid=%t, want %t", schemaOK, test.wantSchema)
			}
			if terminalOK != test.wantTerminal {
				t.Errorf("terminal event=%t, want %t", terminalOK, test.wantTerminal)
			}
		})
	}
}

func TestLiveChatAndClaudeStreamValidation(t *testing.T) {
	tests := []struct {
		name         string
		api          string
		body         string
		wantModel    string
		wantText     string
		wantSchema   bool
		wantTerminal bool
	}{
		{
			name:      "Chat chunk schema and done marker",
			api:       "Chat",
			body:      `data: {"object":"chat.completion.chunk","model":"gemini-3.8-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"LIVE_MATRIX_OK"},"finish_reason":null}]}` + "\n\n" + "data: [DONE]\n\n",
			wantModel: "gemini-3.8-flash", wantText: "LIVE_MATRIX_OK", wantSchema: true, wantTerminal: true,
		},
		{
			name:         "Chat rejects non chunk object",
			api:          "Chat",
			body:         `data: {"object":"chat.completion","model":"gemini-3.8-flash","choices":[{"delta":{"content":"LIVE_MATRIX_OK"}}]}` + "\n\n" + "data: [DONE]\n\n",
			wantModel:    "gemini-3.8-flash",
			wantText:     "LIVE_MATRIX_OK",
			wantTerminal: true,
		},
		{
			name: "Claude message lifecycle and text delta",
			api:  "Claude Messages",
			body: `data: {"type":"message_start","message":{"type":"message","role":"assistant","model":"claude-haiku-5.5","content":[]}}` + "\n\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"LIVE_MATRIX_OK"}}` + "\n\n" +
				`data: {"type":"content_block_stop","index":0}` + "\n\n" +
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null}}` + "\n\n" +
				`data: {"type":"message_stop"}` + "\n\n",
			wantModel: "claude-haiku-5.5", wantText: "LIVE_MATRIX_OK", wantSchema: true, wantTerminal: true,
		},
		{
			name: "Claude rejects missing root identity",
			api:  "Claude Messages",
			body: `data: {"type":"message_start","message":{"type":"message","role":"user","content":[]}}` + "\n\n" +
				`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"LIVE_MATRIX_OK"}}` + "\n\n" +
				`data: {"type":"message_stop"}` + "\n\n",
			wantText:     "LIVE_MATRIX_OK",
			wantTerminal: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text, returnedModel, schemaOK, terminalOK := validateLiveResponse(test.api, true, "text/event-stream", []byte(test.body))
			if text != test.wantText {
				t.Errorf("stream text=%q, want %q", text, test.wantText)
			}
			if returnedModel != test.wantModel {
				t.Errorf("stream model=%q, want exact requested model %q", returnedModel, test.wantModel)
			}
			if schemaOK != test.wantSchema {
				t.Errorf("schema valid=%t, want %t", schemaOK, test.wantSchema)
			}
			if terminalOK != test.wantTerminal {
				t.Errorf("terminal event=%t, want %t", terminalOK, test.wantTerminal)
			}
		})
	}
}

func discoverLiveProfile(t *testing.T, storageJSON []byte) liveProfile {
	t.Helper()
	catalog, status, err := probeLiveModelCatalog(storageJSON, "direct_oauth")
	if err != nil {
		t.Fatalf("Copilot CLI direct_oauth catalog discovery failed with status %d", status)
	}
	count := 0
	for model := range liveCopilotRoutes {
		if _, ok := catalog[model]; ok {
			count++
		}
	}
	t.Logf("credential profile accepted: source=copilot-cli-keychain auth_mode=direct_oauth catalog_rows=%d exact_target_models=%d", len(catalog), count)
	return liveProfile{AuthMode: "direct_oauth", StorageJSON: storageJSON, Catalog: catalog}
}

func probeLiveModelCatalog(storageJSON []byte, authMode string) (map[string][]string, int, error) {
	callback := &liveCatalogHost{client: &http.Client{Timeout: 30 * time.Second}}
	service := provider.New(callback)
	if err := service.Configure([]byte("auth_mode: " + authMode + "\n")); err != nil {
		return nil, 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if _, err := service.ModelsForAuth(ctx, "live-matrix", pluginapi.AuthModelRequest{AuthID: "live-matrix", StorageJSON: storageJSON}); err != nil {
		var statusErr *provider.StatusError
		if errors.As(err, &statusErr) {
			return nil, statusErr.HTTPStatus, err
		}
		return nil, 0, err
	}
	body := callback.modelCatalog()
	var catalog liveCatalogResponse
	if len(body) == 0 || json.Unmarshal(body, &catalog) != nil {
		return nil, 0, errors.New("model catalog response was unavailable")
	}
	models := make(map[string][]string, len(catalog.Data))
	for _, model := range catalog.Data {
		models[model.ID] = append([]string(nil), model.SupportedEndpoints...)
	}
	return models, http.StatusOK, nil
}

func (h *liveCatalogHost) Do(ctx context.Context, _ string, request transport.Request) (transport.Response, error) {
	upstreamRequest, err := http.NewRequestWithContext(ctx, request.Method, request.URL, bytes.NewReader(request.Body))
	if err != nil {
		return transport.Response{}, errors.New("could not construct Copilot catalog callback")
	}
	upstreamRequest.Header = request.Headers.Clone()
	response, err := h.client.Do(upstreamRequest)
	if err != nil {
		return transport.Response{}, errors.New("Copilot catalog callback failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return transport.Response{}, errors.New("Copilot catalog response could not be read")
	}
	if parsed, parseErr := url.Parse(request.URL); parseErr == nil && strings.HasSuffix(parsed.Path, "/models") && response.StatusCode >= 200 && response.StatusCode < 300 {
		h.mu.Lock()
		h.body = append([]byte(nil), body...)
		h.mu.Unlock()
	}
	return transport.Response{StatusCode: response.StatusCode, Headers: response.Header.Clone(), Body: body}, nil
}

func (*liveCatalogHost) OpenStream(context.Context, string, transport.Request) (transport.Stream, error) {
	return transport.Stream{}, errors.New("catalog discovery does not open streams")
}

func (*liveCatalogHost) ReadStream(context.Context, string) (transport.StreamChunk, error) {
	return transport.StreamChunk{}, errors.New("catalog discovery does not read streams")
}

func (*liveCatalogHost) CloseStream(context.Context, string) error {
	return nil
}

func (*liveCatalogHost) Emit(context.Context, string, []byte) error {
	return nil
}

func (*liveCatalogHost) CloseOutput(context.Context, string, string) {}

func (h *liveCatalogHost) modelCatalog() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]byte(nil), h.body...)
}

func liveEndpointOverrides(catalog map[string][]string) map[string]string {
	_ = catalog // Catalog values are logged separately; the matrix pins every assigned protocol route.
	overrides := make(map[string]string, len(liveCopilotRoutes))
	for model, endpoint := range liveCopilotRoutes {
		overrides[model] = endpoint
	}
	return overrides
}

func readCopilotCLIKeychainToken() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", "copilot-cli", "-w").Output()
	if err != nil {
		return "", errors.New("Copilot CLI Keychain entry is unavailable")
	}
	token := strings.TrimSpace(string(output))
	if token == "" {
		return "", errors.New("Copilot CLI Keychain entry is empty")
	}
	return token, nil
}

func liveGitHubIdentity(token string) (string, int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/copilot_internal/user", nil)
	if err != nil {
		return "", 0, errors.New("could not construct GitHub identity request")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "token "+token)
	request.Header.Set("User-Agent", "copilot")
	request.Header.Set("X-GitHub-Api-Version", "2025-04-01")
	response, err := (&http.Client{Timeout: 18 * time.Second}).Do(request)
	if err != nil {
		return "", 0, errors.New("GitHub identity request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", 0, errors.New("GitHub identity request was rejected")
	}
	var identity struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&identity); err != nil || strings.TrimSpace(identity.Login) == "" || identity.ID <= 0 {
		return "", 0, errors.New("GitHub identity response was incomplete")
	}
	return identity.Login, identity.ID, nil
}

func liveCopilotStorage(token, login string, userID int64) ([]byte, error) {
	rootKey := make([]byte, sha256.Size)
	keyID := make([]byte, 16)
	if _, err := rand.Read(rootKey); err != nil {
		return nil, errors.New("could not create temporary continuity material")
	}
	if _, err := rand.Read(keyID); err != nil {
		return nil, errors.New("could not create temporary continuity material")
	}
	fingerprint := sha256.Sum256([]byte(token))
	storage := map[string]any{
		"type":                "copilot",
		"github_access_token": token,
		"github_login":        login,
		"github_user_id":      userID,
		"continuity_keyring": map[string]any{
			"version":                1,
			"key_id":                 hex.EncodeToString(keyID),
			"account_id":             userID,
			"root_key":               base64.RawURLEncoding.EncodeToString(rootKey),
			"credential_fingerprint": hex.EncodeToString(fingerprint[:]),
		},
	}
	body, err := json.Marshal(storage)
	if err != nil {
		return nil, errors.New("could not encode temporary auth state")
	}
	return body, nil
}

func startLiveNativeHost(t *testing.T, binary string, storageJSON []byte, authMode string, endpointOverrides map[string]string) (string, func()) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal("could not create live host root")
	}
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("could not reserve live host port")
	}
	port := portListener.Addr().(*net.TCPAddr).Port
	if err := portListener.Close(); err != nil {
		t.Fatal("could not release live host port")
	}
	pluginDir := filepath.Join(root, "plugins", runtime.GOOS, runtime.GOARCH)
	if err := os.MkdirAll(pluginDir, 0700); err != nil {
		t.Fatal("could not create live plugin directory")
	}
	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	if runtime.GOOS == "windows" {
		ext = ".dll"
	}
	plugin, err := os.ReadFile(filepath.Join("..", "build", "plugins", runtime.GOOS, runtime.GOARCH, "cliproxyapi-copilot"+ext))
	if err != nil {
		t.Fatal("live native plugin artifact is unavailable")
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "cliproxyapi-copilot"+ext), plugin, 0600); err != nil {
		t.Fatal("could not stage live native plugin")
	}
	authDir := filepath.Join(root, "auths")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal("could not create task-owned auth directory")
	}
	if err := os.WriteFile(filepath.Join(authDir, "live-matrix.json"), storageJSON, 0600); err != nil {
		t.Fatal("could not write task-owned auth state")
	}
	config := nativeTemplateConfig(t)
	server := nativeMap(t, config["server"])
	server["host"] = "127.0.0.1"
	server["port"] = port
	management := nativeMap(t, config["management"])
	management["allow-remote"] = false
	management["disable-control-panel"] = true
	management["secret-key"] = "live-matrix-management-secret"
	access := nativeMap(t, config["access"])
	access["api-keys"] = []string{liveCopilotClientKey}
	oauth := nativeMap(t, config["oauth"])
	oauth["auth-dir"] = authDir
	plugins := nativeMap(t, config["plugins"])
	plugins["dir"] = filepath.Join(root, "plugins")
	pluginConfigs := nativeMap(t, plugins["configs"])
	pluginConfig := nativeMap(t, pluginConfigs["cliproxyapi-copilot"])
	pluginConfig["auth_mode"] = authMode
	pluginConfig["model_endpoint_overrides"] = endpointOverrides
	routing := nativeMap(t, config["routing"])
	retry := nativeMap(t, routing["retry"])
	retry["request-retry"] = 0
	configBody, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal("could not encode live host config")
	}
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configPath, configBody, 0600); err != nil {
		t.Fatal("could not write live host config")
	}
	logFile, err := os.OpenFile(filepath.Join(root, "host.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal("could not create private host log")
	}
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binary, "--config", configPath, "--local-model")
	command.Env = liveChildEnvironment(root)
	command.Dir = root
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		t.Fatal("could not start disposable native host")
	}
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			_ = command.Wait()
			_ = logFile.Close()
		})
	}
	t.Cleanup(stop)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("disposable native host did not become ready; private host log was withheld")
		case <-ticker.C:
			request, err := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
			if err != nil {
				continue
			}
			request.Header.Set("Authorization", "Bearer "+liveCopilotClientKey)
			response, err := client.Do(request)
			if err != nil {
				continue
			}
			body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK && liveModelCatalogHasTargets(body) {
				return base, stop
			}
		}
	}
}

func liveModelCatalogHasTargets(body []byte) bool {
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil {
		return false
	}
	registered := make(map[string]struct{}, len(response.Data))
	for _, model := range response.Data {
		registered[model.ID] = struct{}{}
	}
	for model := range liveCopilotRoutes {
		if _, ok := registered[model]; !ok {
			return false
		}
	}
	return true
}

func liveChildEnvironment(home string) []string {
	filtered := filteredChildEnvironment(os.Environ())
	out := make([]string, 0, len(filtered)+1)
	for _, entry := range filtered {
		if !strings.HasPrefix(strings.ToUpper(entry), "HOME=") {
			out = append(out, entry)
		}
	}
	return append(out, "HOME="+home)
}

func liveMatrixRequest(model, api string, stream bool) (string, map[string]any) {
	prompt := "Reply with exactly LIVE_MATRIX_OK."
	switch api {
	case "Chat":
		return "/v1/chat/completions", map[string]any{
			"model": model, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": prompt}},
		}
	case "Responses":
		return "/v1/responses", map[string]any{
			"model": model, "stream": stream, "store": false, "input": prompt,
		}
	default:
		return "/v1/messages", map[string]any{
			"model": model, "stream": stream, "max_tokens": 64, "messages": []any{map[string]any{"role": "user", "content": prompt}},
		}
	}
}

func liveProxyCall(endpoint string, payload any) ([]byte, int, string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, "", errors.New("could not encode live request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, "", errors.New("could not construct live request")
	}
	request.Header.Set("Authorization", "Bearer "+liveCopilotClientKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Session-Id", "live-copilot-matrix")
	response, err := (&http.Client{Timeout: 95 * time.Second}).Do(request)
	if err != nil {
		return nil, 0, "", errors.New("native host request failed")
	}
	defer response.Body.Close()
	result, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, response.StatusCode, response.Header.Get("Content-Type"), errors.New("native host response could not be read")
	}
	return result, response.StatusCode, response.Header.Get("Content-Type"), nil
}

func validateLiveResponse(api string, stream bool, contentType string, body []byte) (string, string, bool, bool) {
	if stream {
		if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
			return "", "", false, false
		}
		events, done, validStream := parseLiveSSE(body)
		text := ""
		model := ""
		terminal := false
		modelConsistent := true
		responseCompletionValid := false
		for _, event := range events {
			if currentModel := liveStreamModel(api, event); currentModel != "" {
				if model != "" && currentModel != model {
					modelConsistent = false
				}
				model = currentModel
			}
			switch api {
			case "Chat":
				text += liveChatDelta(event)
			case "Responses":
				text += liveResponsesDelta(event)
				if event["type"] == "response.completed" {
					terminal = true
					if response, ok := event["response"].(map[string]any); ok {
						completedModel, modelOK := response["model"].(string)
						if modelOK && completedModel != "" {
							if model != "" && completedModel != model {
								modelConsistent = false
							}
							model = completedModel
						}
						responseCompletionValid = response["object"] == "response" && response["status"] == "completed" && modelOK && completedModel != ""
						if text == "" {
							text = liveResponseText(response)
						}
					}
				}
			case "Claude Messages":
				text += liveClaudeDelta(event)
				if event["type"] == "message_stop" {
					terminal = true
				}
			}
		}
		if api == "Chat" {
			terminal = done
		}
		schemaOK := validStream && len(events) > 0 && modelConsistent
		switch api {
		case "Chat":
			schemaOK = schemaOK && terminal && liveChatStreamValid(events)
		case "Responses":
			schemaOK = schemaOK && responseCompletionValid && model != ""
		case "Claude Messages":
			schemaOK = schemaOK && liveClaudeStreamValid(events)
		}
		return text, model, schemaOK, terminal
	}
	var response map[string]any
	if json.Unmarshal(body, &response) != nil {
		return "", "", false, false
	}
	model, _ := response["model"].(string)
	switch api {
	case "Chat":
		choices, ok := response["choices"].([]any)
		if response["object"] != "chat.completion" || !ok || len(choices) == 0 {
			return "", model, false, false
		}
		choice, ok := choices[0].(map[string]any)
		if !ok {
			return "", model, false, false
		}
		message, ok := choice["message"].(map[string]any)
		if !ok || message["role"] != "assistant" {
			return "", model, false, false
		}
		text, ok := message["content"].(string)
		return text, model, ok, true
	case "Responses":
		if response["status"] != "completed" || response["object"] != "response" {
			return "", model, false, false
		}
		return liveResponseText(response), model, true, true
	default:
		if response["type"] != "message" || response["role"] != "assistant" {
			return "", model, false, false
		}
		return liveContentText(response["content"]), model, true, true
	}
}

func liveStreamModel(api string, event map[string]any) string {
	if model, ok := event["model"].(string); ok {
		return model
	}
	if api == "Responses" {
		if response, ok := event["response"].(map[string]any); ok {
			model, _ := response["model"].(string)
			return model
		}
	}
	if api == "Claude Messages" {
		if message, ok := event["message"].(map[string]any); ok {
			model, _ := message["model"].(string)
			return model
		}
	}
	return ""
}

func parseLiveSSE(body []byte) ([]map[string]any, bool, bool) {
	var events []map[string]any
	done := false
	valid := true
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(data), &event) != nil {
			return events, done, false
		}
		events = append(events, event)
	}
	if scanner.Err() != nil {
		valid = false
	}
	return events, done, valid
}

func liveChatDelta(event map[string]any) string {
	choices, _ := event["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	text, _ := delta["content"].(string)
	return text
}

func liveChatStreamValid(events []map[string]any) bool {
	if len(events) == 0 {
		return false
	}
	for _, event := range events {
		if event["object"] != "chat.completion.chunk" {
			return false
		}
		model, modelOK := event["model"].(string)
		choices, choicesOK := event["choices"].([]any)
		if !modelOK || model == "" || !choicesOK || len(choices) == 0 {
			return false
		}
		for _, value := range choices {
			choice, ok := value.(map[string]any)
			if !ok {
				return false
			}
			if _, ok := choice["delta"].(map[string]any); !ok {
				return false
			}
			if finish, exists := choice["finish_reason"]; exists && finish != nil {
				if _, ok := finish.(string); !ok {
					return false
				}
			}
		}
	}
	return true
}

func liveClaudeStreamValid(events []map[string]any) bool {
	starts, stops := 0, 0
	messageModel := ""
	for index, event := range events {
		switch event["type"] {
		case "ping":
		case "message_start":
			starts++
			message, ok := event["message"].(map[string]any)
			if !ok || message["type"] != "message" || message["role"] != "assistant" {
				return false
			}
			messageModel, _ = message["model"].(string)
			if messageModel == "" {
				return false
			}
			if _, ok := message["content"].([]any); !ok {
				return false
			}
		case "content_block_start":
			block, ok := event["content_block"].(map[string]any)
			if !ok || !liveClaudeBlockStartValid(block) {
				return false
			}
		case "content_block_delta":
			delta, ok := event["delta"].(map[string]any)
			if !ok || !liveClaudeDeltaValid(delta) {
				return false
			}
		case "content_block_stop":
			if _, ok := event["index"].(float64); !ok {
				return false
			}
		case "message_delta":
			delta, ok := event["delta"].(map[string]any)
			if !ok {
				return false
			}
			if reason, exists := delta["stop_reason"]; exists && reason != nil {
				if _, ok := reason.(string); !ok {
					return false
				}
			}
		case "message_stop":
			stops++
			if index != len(events)-1 {
				return false
			}
		default:
			return false
		}
	}
	return starts == 1 && stops == 1 && messageModel != ""
}

func liveClaudeBlockStartValid(block map[string]any) bool {
	switch block["type"] {
	case "text":
		_, ok := block["text"].(string)
		return ok
	case "thinking":
		_, ok := block["thinking"].(string)
		return ok
	case "redacted_thinking":
		_, ok := block["data"].(string)
		return ok
	case "tool_use":
		id, idOK := block["id"].(string)
		name, nameOK := block["name"].(string)
		_, inputOK := block["input"].(map[string]any)
		return idOK && id != "" && nameOK && name != "" && inputOK
	default:
		return false
	}
}

func liveClaudeDeltaValid(delta map[string]any) bool {
	switch delta["type"] {
	case "text_delta":
		_, ok := delta["text"].(string)
		return ok
	case "thinking_delta":
		_, ok := delta["thinking"].(string)
		return ok
	case "signature_delta":
		_, ok := delta["signature"].(string)
		return ok
	case "input_json_delta":
		_, ok := delta["partial_json"].(string)
		return ok
	default:
		return false
	}
}

func liveResponsesDelta(event map[string]any) string {
	if event["type"] != "response.output_text.delta" {
		return ""
	}
	text, _ := event["delta"].(string)
	return text
}

func liveClaudeDelta(event map[string]any) string {
	if event["type"] != "content_block_delta" {
		return ""
	}
	delta, _ := event["delta"].(map[string]any)
	text, _ := delta["text"].(string)
	return text
}

func liveResponseText(response map[string]any) string {
	return liveContentText(response["output"])
}

func liveContentText(value any) string {
	var text strings.Builder
	switch content := value.(type) {
	case []any:
		for _, item := range content {
			if object, ok := item.(map[string]any); ok {
				if itemType, _ := object["type"].(string); itemType == "output_text" || itemType == "text" {
					if value, ok := object["text"].(string); ok {
						text.WriteString(value)
					}
				}
				if nested, ok := object["content"]; ok {
					text.WriteString(liveContentText(nested))
				}
			}
		}
	case string:
		text.WriteString(content)
	}
	return text.String()
}

func safeLiveErrorCode(body []byte) string {
	code := safeLiveErrorField(body, "code")
	if code != "" {
		return code
	}
	return safeLiveErrorField(body, "type")
}

func safeLiveErrorType(body []byte) string {
	return safeLiveErrorField(body, "type")
}

func safeLiveErrorField(body []byte, field string) string {
	var response map[string]any
	if json.Unmarshal(body, &response) != nil {
		return ""
	}
	errorObject, _ := response["error"].(map[string]any)
	if value, ok := errorObject[field].(string); ok {
		var safe strings.Builder
		for _, character := range value {
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' || character == '.' {
				safe.WriteRune(character)
			}
		}
		return safe.String()
	}
	return ""
}

func safeLiveErrorClass(body []byte, status int) string {
	var response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &response) == nil && response.Error.Message == fmt.Sprintf("Copilot upstream returned HTTP %d", status) {
		return fmt.Sprintf("copilot_upstream_http_%d", status)
	}
	if errorType := safeLiveErrorType(body); errorType != "" {
		return "public_" + errorType
	}
	return "unknown"
}

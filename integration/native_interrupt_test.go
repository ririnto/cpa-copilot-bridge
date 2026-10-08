package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

func TestNativeHostResponseInterruptUnsupported(t *testing.T) {
	binary := os.Getenv("CPA_BINARY")
	if binary == "" {
		t.Skip("set CPA_BINARY to the prepared CLIProxyAPI v8 server for this unsupported-control-frame diagnosis")
	}
	t.Run("PluginNormalization", func(t *testing.T) {
		state := newNativeFixture(t)
		upstream := httptest.NewServer(state)
		t.Cleanup(upstream.Close)
		base, stop := startNativeInterruptDiagnosticHost(t, binary, upstream.URL, true)
		defer stop()
		connection := dialNativeResponsesWebsocket(t, base)
		request := nativeWebsocketRequest(t, "gpt-6-luna", "/responses")
		if err := connection.WriteJSON(request); err != nil {
			t.Fatal(err)
		}
		completed := nativeCompletedResponse(t, readPluginResponsesWebsocket(t, connection))
		before := upstreamRequestCount(state)
		responseID, ok := completed["id"].(string)
		if !ok || responseID == "" {
			t.Fatal("plugin response did not expose its response ID")
		}
		assertNativeInterruptRejection(t, connection, responseID)
		if after := upstreamRequestCount(state); after != before {
			t.Fatalf("unsupported interrupt dispatched %d extra upstream inference requests", after-before)
		}
	})
	t.Run("ActiveNativeDuplex", func(t *testing.T) {
		requests := make(chan []byte, 4)
		finished := make(chan struct{})
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			connection, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer close(finished)
			defer connection.Close()
			_, request, err := connection.ReadMessage()
			if err != nil {
				return
			}
			requests <- request
			if err := writeNativeWebsocketJSON(connection, map[string]any{"type": "response.created", "sequence_number": 0, "response": map[string]any{"id": "resp-interrupted", "object": "response", "status": "in_progress", "model": "fixture-summary", "output": []any{}}}); err != nil {
				return
			}
			for {
				_, request, err := connection.ReadMessage()
				if err != nil {
					return
				}
				requests <- request
			}
		}))
		t.Cleanup(upstream.Close)
		base, stop := startNativeInterruptDiagnosticHost(t, binary, upstream.URL, false)
		defer stop()
		connection := dialNativeResponsesWebsocket(t, base)
		if err := connection.WriteJSON(map[string]any{"type": "response.create", "model": "fixture-summary", "input": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Synthetic interrupt diagnosis."}}}}}); err != nil {
			t.Fatal(err)
		}
		first := receiveNativeWebsocketRequest(t, requests)
		if !strings.Contains(string(first), "response.create") {
			t.Fatalf("upstream initial frame was not response.create: %s", first)
		}
		var created map[string]any
		if err := connection.ReadJSON(&created); err != nil {
			t.Fatal(err)
		}
		if created["type"] != "response.created" {
			t.Fatalf("initial event = %+v, want response.created", created)
		}
		assertNativeInterruptRejection(t, connection, "resp-interrupted")
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
		stop()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("synthetic upstream reader did not terminate")
		}
		select {
		case forwarded := <-requests:
			t.Fatalf("host unexpectedly forwarded interrupt upstream: %s", forwarded)
		default:
		}
	})
}

func assertNativeInterruptRejection(t *testing.T, connection *websocket.Conn, responseID string) {
	t.Helper()
	request, err := json.Marshal(map[string]string{"type": "response.interrupt", "response_id": responseID, "mode": "discard_partial_items"})
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteMessage(websocket.TextMessage, request); err != nil {
		t.Fatal(err)
	}
	if deadline, ok := t.Deadline(); ok {
		if err := connection.SetReadDeadline(deadline.Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	_, response, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "error" || event.Status != http.StatusBadRequest || event.Error.Message != "unsupported websocket request type: response.interrupt" {
		t.Fatalf("unsupported interrupt response = %s", response)
	}
	if directory := liveDebugArtifactDirectory(t, "interrupt-body"); directory != "" {
		for name, body := range map[string][]byte{"client-request.json": request, "client-response.json": response} {
			if err := os.WriteFile(filepath.Join(directory, name), body, 0600); err != nil {
				t.Fatal("could not retain synthetic interrupt wire bodies")
			}
		}
		t.Logf("retained original synthetic interrupt wire bodies: %s", directory)
	}
	t.Logf("diagnostic unsupported result; original client frame=%s original host error=%s", request, response)
}

func startNativeInterruptDiagnosticHost(t *testing.T, binary, upstream string, plugin bool) (string, func()) {
	t.Helper()
	root := t.TempDir()
	debugDirectory := liveDebugArtifactDirectory(t, "interrupt-host")
	if debugDirectory == "" {
		debugDirectory = root
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if plugin {
		pluginDirectory := filepath.Join(root, "plugins", runtime.GOOS, runtime.GOARCH)
		if err := os.MkdirAll(pluginDirectory, 0700); err != nil {
			t.Fatal(err)
		}
		extension := map[string]string{"darwin": ".dylib", "windows": ".dll"}[runtime.GOOS]
		if extension == "" {
			extension = ".so"
		}
		artifact, err := os.ReadFile(filepath.Join("..", "build", "plugins", runtime.GOOS, runtime.GOARCH, "cliproxyapi-copilot"+extension))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pluginDirectory, "cliproxyapi-copilot"+extension), artifact, 0600); err != nil {
			t.Fatal(err)
		}
		body, authDirectory := nativeRuntimeConfig(t, root, port, upstream, "")
		if err := os.MkdirAll(authDirectory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(authDirectory, "fixture.json"), nativeAuthFixtureJSON(t), 0600); err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(body, &config); err != nil {
			t.Fatal(err)
		}
	} else {
		config = map[string]any{"config-version": 8, "server": map[string]any{"host": "127.0.0.1", "port": port}, "management": map[string]any{"disable-control-panel": true}, "access": map[string]any{"api-keys": []string{"fixture-client-key"}}, "oauth": map[string]any{"auth-dir": filepath.Join(root, "auths")}, "codex-api-key": []any{map[string]any{"api-key": "fixture-codex-upstream", "base-url": upstream, "websockets": true, "models": []any{map[string]any{"name": "fixture-summary", "alias": "fixture-summary"}}}}}
		config["upstream"] = map[string]any{"codex": map[string]any{"response-steering": true, "stream-bootstrap-buffering": false}}
	}
	config["observability"] = map[string]any{"logs": map[string]any{"debug": true, "logging-to-file": true, "request-log": true, "logs-max-total-size-mb": 0, "error-logs-max-files": 0}}
	body, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.OpenFile(filepath.Join(debugDirectory, "host.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binary, "--config", configPath, "--local-model")
	command.Env = append(filteredChildEnvironment(os.Environ()), "WRITABLE_PATH="+debugDirectory)
	command.Dir = root
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		t.Fatal(err)
	}
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			_ = command.Process.Signal(os.Interrupt)
			exited := make(chan error, 1)
			go func() { exited <- command.Wait() }()
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-exited:
			case <-timer.C:
				cancel()
				<-exited
			}
			timer.Stop()
			cancel()
			_ = logFile.Close()
			if err := scrubLiveHostLogs(debugDirectory); err != nil {
				t.Error("could not sanitize interrupt diagnostic host logs")
			}
			t.Logf("retained synthetic interrupt DEBUG/request logs: %s", debugDirectory)
		})
	}
	t.Cleanup(stop)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 3 * time.Second}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatal("synthetic interrupt host did not expose models; retained host logs available")
		case <-ticker.C:
			request, err := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer fixture-client-key")
			response, err := client.Do(request)
			if err == nil {
				body, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				startupLog, startupErr := os.ReadFile(filepath.Join(debugDirectory, "logs", "main.log"))
				if err == nil && startupErr == nil && strings.Contains(string(startupLog), "file watcher started") && response.StatusCode == http.StatusOK && (plugin && strings.Contains(string(body), "gpt-6-luna") || !plugin && strings.Contains(string(body), "fixture-summary")) {
					return base, stop
				}
			}
		}
	}
}

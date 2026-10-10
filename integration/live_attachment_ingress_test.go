//go:build !windows

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type liveAttachmentIngressCapture struct {
	Sequence     int         `json:"sequence"`
	Dispatched   bool        `json:"dispatched"`
	Request      liveHTTPLog `json:"original_client_request"`
	Response     liveHTTPLog `json:"local_host_response"`
	ErrorClass   string      `json:"error_class,omitempty"`
	BodyComplete bool        `json:"body_complete"`
}

type liveAttachmentIngress struct {
	mu             sync.Mutex
	server         *httptest.Server
	client         *http.Client
	gate           *liveServerToolGate
	directory      string
	upstream       string
	clientID       string
	sequence       int
	denials        int
	failure        error
	captureFailure bool
	refusalStatus  int
}

func newLiveAttachmentIngress(t *testing.T, gate *liveServerToolGate, base, directory, clientID string) *liveAttachmentIngress {
	t.Helper()
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Path != "" || parsed.User != nil {
		t.Fatal("original-client ingress must forward only to the owned local CPA host")
	}
	if err := os.MkdirAll(directory, 0700); err != nil || os.Chmod(directory, 0700) != nil {
		t.Fatal("could not create private original-client ingress capture root")
	}
	ingress := &liveAttachmentIngress{client: &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: liveOneShotTransport{}}, gate: gate, directory: directory, upstream: base, clientID: clientID}
	ingress.server = httptest.NewServer(http.HandlerFunc(ingress.serveHTTP))
	t.Cleanup(ingress.server.Close)
	return ingress
}

func (i *liveAttachmentIngress) URL() string { return i.server.URL }

func (i *liveAttachmentIngress) Close() { i.server.Close() }

func (i *liveAttachmentIngress) state() (int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.denials, i.failure
}

func (i *liveAttachmentIngress) failureState() (bool, int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.captureFailure, i.refusalStatus
}

func (i *liveAttachmentIngress) fail(err error) {
	i.mu.Lock()
	i.captureFailure = true
	if i.failure == nil {
		i.failure = err
	}
	i.mu.Unlock()
}

func (i *liveAttachmentIngress) refuse(status int) {
	i.mu.Lock()
	i.refusalStatus = status
	if i.failure == nil {
		i.failure = errors.New("original client local host returned a refusal")
	}
	i.mu.Unlock()
}

func (i *liveAttachmentIngress) reserve(method, path string) (int, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.sequence++
	allowedPath := method == http.MethodPost && (i.clientID == "claude" && path == "/v1/messages" || i.clientID == "codex" && path == "/v1/responses")
	if i.failure != nil || !allowedPath {
		i.denials++
		return i.sequence, false
	}
	return i.sequence, true
}

func (i *liveAttachmentIngress) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	const maxBody = 16 << 20
	sequence, allowed := i.reserve(request.Method, request.URL.Path)
	if !allowed {
		capture := liveAttachmentIngressCapture{Sequence: sequence, Dispatched: false, Request: liveHTTPLog{Method: request.Method, URL: request.URL.String(), Host: request.Host, Proto: request.Proto, Headers: i.gate.redactedHeaders(request.Header), Body: "[DENIED BODY OMITTED]"}, Response: liveHTTPLog{Status: http.StatusForbidden}, ErrorClass: "original_client_path_not_prepared"}
		if err := i.persist(capture); err != nil {
			i.fail(errors.New("original client denial capture was not durable"))
		}
		http.Error(writer, "original client path was not prepared", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		retained := body[:min(len(body), maxBody)]
		capture := liveAttachmentIngressCapture{Sequence: sequence, Dispatched: false, Request: liveHTTPLog{Method: request.Method, URL: request.URL.String(), Host: request.Host, Proto: request.Proto, Headers: i.gate.redactedHeaders(request.Header), Body: i.gate.redactedBody(retained), ContentLength: request.ContentLength, ContentEncoding: request.Header.Get("Content-Encoding"), TransferEncoding: append([]string(nil), request.TransferEncoding...)}, Response: liveHTTPLog{Status: http.StatusRequestEntityTooLarge}, ErrorClass: "original_client_request_unavailable", BodyComplete: false}
		if persistErr := i.persist(capture); persistErr != nil {
			i.fail(errors.New("original client partial request capture was not durable"))
		}
		i.fail(errors.New("original client request capture was unavailable"))
		http.Error(writer, "original client request was unavailable", http.StatusRequestEntityTooLarge)
		return
	}
	capture := liveAttachmentIngressCapture{Sequence: sequence, Request: liveHTTPLog{Method: request.Method, URL: request.URL.String(), Host: request.Host, Proto: request.Proto, Headers: i.gate.redactedHeaders(request.Header), Body: i.gate.redactedBody(body), ContentLength: request.ContentLength, ContentEncoding: request.Header.Get("Content-Encoding"), TransferEncoding: append([]string(nil), request.TransferEncoding...)}, BodyComplete: true}
	if err := i.persist(capture); err != nil {
		i.fail(errors.New("original client pre-dispatch capture was not durable"))
		http.Error(writer, "original client capture failed", http.StatusBadGateway)
		return
	}
	upstreamURL := i.upstream + request.URL.RequestURI()
	forward, err := http.NewRequestWithContext(request.Context(), request.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		i.fail(errors.New("original client local forward could not be prepared"))
		http.Error(writer, "original client local forward failed", http.StatusBadGateway)
		return
	}
	forward.Header = request.Header.Clone()
	removeHopHeaders(forward.Header)
	forward.Host = forward.URL.Host
	capture.Dispatched = true
	response, err := i.client.Do(forward)
	if err != nil {
		capture.ErrorClass = "local_host_transport_error"
		i.fail(errors.New("original client local host transport failed"))
		_ = i.persist(capture)
		http.Error(writer, "local host transport failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		i.refuse(response.StatusCode)
	}
	copyHTTPHeaders(writer.Header(), response.Header)
	removeHopHeaders(writer.Header())
	writer.Header().Del("Content-Length")
	writer.WriteHeader(response.StatusCode)
	capture.Response = liveHTTPLog{Proto: response.Proto, Status: response.StatusCode, Headers: i.gate.redactedHeaders(response.Header), ContentLength: response.ContentLength, ContentEncoding: response.Header.Get("Content-Encoding"), TransferEncoding: append([]string(nil), response.TransferEncoding...)}
	var retained bytes.Buffer
	chunk := make([]byte, 32<<10)
	for {
		n, readErr := response.Body.Read(chunk)
		if n > 0 {
			remaining := maxBody - retained.Len()
			if remaining > 0 {
				retained.Write(chunk[:min(n, remaining)])
			}
			if n > remaining {
				capture.BodyComplete = false
				i.fail(errors.New("original client response capture was truncated"))
			}
			if _, writeErr := writer.Write(chunk[:n]); writeErr != nil {
				capture.ErrorClass = "original_client_response_write_error"
				i.fail(errors.New("original client response write failed"))
				break
			}
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			capture.ErrorClass = "local_host_response_read_error"
			i.fail(errors.New("original client local host response read failed"))
			break
		}
	}
	capture.Response.Body = i.gate.redactedBody(retained.Bytes())
	if err := i.persist(capture); err != nil {
		i.fail(errors.New("original client response capture was not durable"))
	}
}

func (i *liveAttachmentIngress) persist(capture liveAttachmentIngressCapture) error {
	content, err := json.MarshalIndent(capture, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(i.directory, fmt.Sprintf("client-hop-%03d.json", capture.Sequence))
	temporary, err := os.CreateTemp(i.directory, ".client-hop-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0600); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(i.directory)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (i *liveAttachmentIngress) captures() ([]liveAttachmentIngressCapture, error) {
	paths, err := filepath.Glob(filepath.Join(i.directory, "client-hop-*.json"))
	if err != nil {
		return nil, err
	}
	result := make([]liveAttachmentIngressCapture, 0, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var capture liveAttachmentIngressCapture
		if err := json.Unmarshal(body, &capture); err != nil {
			return nil, err
		}
		result = append(result, capture)
	}
	return result, nil
}

func TestAttachmentIngressRejectsUnpreparedPathsBeforeForwarding(t *testing.T) {
	gate := newLiveServerToolGate(t, t.TempDir())
	ingress := newLiveAttachmentIngress(t, gate, "http://127.0.0.1:1", t.TempDir(), "claude")
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ingress.URL()+"/v1/responses", strings.NewReader(`{"model":"gpt-6-luna"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("unprepared original-client path was forwarded")
	}
	denials, _ := ingress.state()
	if denials != 1 {
		t.Fatal("unprepared original-client path was not counted")
	}
}

func TestAttachmentIngressPersistenceFailureStopsLocalForward(t *testing.T) {
	var forwarded int
	localHost := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		forwarded++
		writer.WriteHeader(http.StatusOK)
	}))
	defer localHost.Close()
	gate := newLiveServerToolGate(t, t.TempDir())
	ingress := newLiveAttachmentIngress(t, gate, localHost.URL, t.TempDir(), "claude")
	blockedDirectory := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedDirectory, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	ingress.directory = blockedDirectory
	request, err := http.NewRequest(http.MethodPost, ingress.URL()+"/v1/messages", strings.NewReader(`{"model":"gpt-6-luna"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	denials, failure := ingress.state()
	if response.StatusCode != http.StatusBadGateway || forwarded != 0 || failure == nil || denials != 0 {
		t.Fatal("undurable original-client request was forwarded")
	}
	secondRequest, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ingress.URL()+"/v1/messages", strings.NewReader(`{"model":"gpt-6-luna"}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := http.DefaultClient.Do(secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	denials, _ = ingress.state()
	if second.StatusCode != http.StatusForbidden || forwarded != 0 || denials != 1 {
		t.Fatal("original-client capture failure did not stay fail-closed")
	}
}

func TestAttachmentIngressLocalConversionRefusalIsNotCaptureFailure(t *testing.T) {
	localHost := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = writer.Write([]byte(`{"error":{"type":"unsupported_feature"}}`))
	}))
	defer localHost.Close()
	gate := newLiveServerToolGate(t, t.TempDir())
	ingress := newLiveAttachmentIngress(t, gate, localHost.URL, t.TempDir(), "codex")
	request, err := http.NewRequest(http.MethodPost, ingress.URL()+"/v1/responses", strings.NewReader(`{"model":"claude-haiku-5.5"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	denials, failure := ingress.state()
	captureFailed, refusalStatus := ingress.failureState()
	var captures []liveAttachmentIngressCapture
	var readErr error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		captures, readErr = ingress.captures()
		if readErr == nil && len(captures) == 1 && captures[0].Response.Status == http.StatusUnprocessableEntity {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("local refusal: status=%d refusal=%d capture_failed=%t failure_present=%t read_error=%t denials=%d captures=%d", response.StatusCode, refusalStatus, captureFailed, failure != nil, readErr != nil, denials, len(captures))
	if len(captures) == 1 {
		t.Logf("local refusal capture: dispatched=%t complete=%t status=%d error_class=%s", captures[0].Dispatched, captures[0].BodyComplete, captures[0].Response.Status, captures[0].ErrorClass)
	}
	if response.StatusCode != http.StatusUnprocessableEntity || refusalStatus != http.StatusUnprocessableEntity || captureFailed || failure == nil || readErr != nil || denials != 0 || len(captures) != 1 || !captures[0].Dispatched || !captures[0].BodyComplete || captures[0].Response.Status != http.StatusUnprocessableEntity {
		t.Fatal("local conversion refusal was mislabeled or not retained")
	}
}

func TestAttachmentIngressOversizedRequestRetainsPartialCapture(t *testing.T) {
	var forwarded atomic.Int32
	localHost := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		forwarded.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer localHost.Close()
	gate := newLiveServerToolGate(t, t.TempDir())
	ingress := newLiveAttachmentIngress(t, gate, localHost.URL, t.TempDir(), "claude")
	request, err := http.NewRequest(http.MethodPost, ingress.URL()+"/v1/messages", strings.NewReader(strings.Repeat("x", (16<<20)+1)))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	captures, err := ingress.captures()
	if err != nil {
		t.Fatal(err)
	}
	_, failure := ingress.state()
	if response.StatusCode != http.StatusRequestEntityTooLarge || forwarded.Load() != 0 || failure == nil || len(captures) != 1 {
		t.Fatal("oversized original-client request did not fail closed with one retained capture")
	}
	capture := captures[0]
	if capture.Dispatched || capture.BodyComplete || capture.ErrorClass != "original_client_request_unavailable" || capture.Response.Status != http.StatusRequestEntityTooLarge || len(capture.Request.Body) != 16<<20 {
		t.Fatal("oversized original-client request did not retain its bounded prefix and failure status")
	}
}

func TestAttachmentPacketTruncationStopsConcurrentDispatchBeforeEOF(t *testing.T) {
	t.Setenv("CPA_LIVE_CLIENT_ATTACHMENTS", "1")
	gate := newLiveServerToolGate(t, t.TempDir())
	gate.publicAPI, _ = url.Parse("https://api.githubcopilot.com")
	if err := gate.setPhaseCell("X", "claude-gpt-png"); err != nil {
		t.Fatal(err)
	}
	gate.setFreshClaim("inference", "X/claude-gpt-png")
	released := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(released) }) }
	defer release()
	var upstreamCalls atomic.Int32
	gate.client.Transport = liveToolRoundTrip(func(*http.Request) (*http.Response, error) {
		upstreamCalls.Add(1)
		reader, writer := io.Pipe()
		go func() {
			_, _ = io.WriteString(writer, strings.Repeat("x", (16<<20)+1))
			<-released
			_ = writer.Close()
		}()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: reader, ContentLength: -1}, nil
	})
	request, err := http.NewRequest(http.MethodPost, gate.server.URL+"/responses", strings.NewReader(`{"model":"gpt-6-luna","reasoning":{"effort":"medium"}}`))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.CopyN(io.Discard, response.Body, 16<<20); err != nil {
		t.Fatal(err)
	}
	var captureErr error
	deadline := time.Now().Add(2 * time.Second)
	for captureErr == nil && time.Now().Before(deadline) {
		_, _, _, _, captureErr = gate.countsFor("X/claude-gpt-png")
		if captureErr == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if captureErr == nil {
		t.Fatal("capture overflow was not made sticky while the upstream stream remained open")
	}
	if _, _, _, allowed := gate.reserve("/responses"); allowed || upstreamCalls.Load() != 1 {
		t.Fatal("concurrent inference escaped after capture overflow but before EOF")
	}
	release()
	additional, _ := io.Copy(io.Discard, response.Body)
	if additional != 0 {
		t.Fatal("over-limit upstream bytes were forwarded after capture failure")
	}
}

func TestAttachmentPacket401RecoveryResetsAfterSuccessfulToolTurn(t *testing.T) {
	t.Setenv("CPA_LIVE_CLIENT_ATTACHMENTS", "1")
	gate := newLiveServerToolGate(t, t.TempDir())
	gate.publicAPI, _ = url.Parse("https://api.githubcopilot.com")
	if err := gate.setPhaseCell("X", "claude-gpt-pdf"); err != nil {
		t.Fatal(err)
	}
	gate.setFreshClaim("inference", "X/claude-gpt-pdf")
	statuses := []int{http.StatusUnauthorized, http.StatusOK, http.StatusUnauthorized, http.StatusOK}
	var calls atomic.Int32
	gate.client.Transport = liveToolRoundTrip(func(*http.Request) (*http.Response, error) {
		index := int(calls.Add(1)) - 1
		if index >= len(statuses) {
			return nil, errors.New("unexpected upstream request")
		}
		return &http.Response{StatusCode: statuses[index], Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":"fixture"}`)), ContentLength: -1}, nil
	})
	for index, want := range statuses {
		request, err := http.NewRequest(http.MethodPost, gate.server.URL+"/responses", strings.NewReader(`{"model":"gpt-6-luna","reasoning":{"effort":"medium"}}`))
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("logical request %d status = %d, want %d", index+1, response.StatusCode, want)
		}
	}
	count, total, _, _, captureErr := gate.countsFor("X/claude-gpt-pdf")
	if count != 4 || total != 4 || captureErr != nil || calls.Load() != 4 {
		t.Fatal("distinct recovered tool turns were blocked or not durably counted")
	}
}

func TestAttachmentPacketStreamReadFailureClassesRemainNonAccepting(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{nil, "active"},
		{context.Canceled, "context_canceled"},
		{context.DeadlineExceeded, "context_deadline_exceeded"},
		{io.ErrUnexpectedEOF, "unexpected_eof"},
		{errors.New("fixture"), "other_read_error"},
	} {
		if got := liveSafeStreamReadErrorKind(test.err); got != test.want {
			t.Fatalf("safe stream read classification = %q, want %q", got, test.want)
		}
	}
}

func TestAttachmentPacketDownstreamCancelRequiresCompleteForwardedTerminal(t *testing.T) {
	terminal := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"status\":\"completed\",\"model\":\"gpt-6-luna\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1},\"output\":[{\"type\":\"function_call\",\"status\":\"completed\",\"name\":\"Read\",\"call_id\":\"call-a\",\"arguments\":\"{\\\"file_path\\\":\\\"fixture.png\\\"}\"}]}}\n\n"
	if !liveCompleteResponseTerminal([]byte(terminal), "gpt-6-luna") || !liveCompleteResponseTerminal([]byte(terminal+"\n \n\n"), "gpt-6-luna") || liveCompleteResponseTerminal([]byte(strings.TrimSuffix(terminal, "\n"))) || liveCompleteResponseTerminal([]byte(strings.Replace(terminal, `"status":"completed"`, `"status":"incomplete"`, 1))) {
		t.Fatal("complete framed successful function-call terminal was not distinguished from a partial or incomplete stream")
	}
	for _, changed := range []string{
		strings.Replace(terminal, `"response":{"object"`, `"error":{"message":"fixture"},"response":{"object"`, 1),
		strings.Replace(terminal, `"status":"completed","model"`, `"status":"completed","error":{"message":"fixture"},"model"`, 1),
		strings.Replace(terminal, `"status":"completed","model"`, `"status":"completed","incomplete_details":{"reason":"max_output_tokens"},"model"`, 1),
		strings.Replace(terminal, `"arguments":"{\"file_path\":\"fixture.png\"}"`, `"arguments":"null"`, 1),
		strings.Replace(terminal, `"model":"gpt-6-luna"`, `"model":"other"`, 1),
	} {
		if liveCompleteResponseTerminal([]byte(changed), "gpt-6-luna") {
			t.Fatal("contradictory completed terminal was accepted")
		}
	}
	good := liveServerToolCapture{ErrorClass: "upstream_response_read_error", StreamReadErrorKind: "context_canceled", StreamTerminalComplete: true, StreamTerminalForwarded: true, StreamTerminalBytes: len(terminal), StreamForwardedBytes: len(terminal), StreamFlushedBytes: len(terminal), StreamContextAtTerminal: "active"}
	if !liveSemanticDownstreamCancellation(good, context.Canceled, context.Canceled) {
		t.Fatal("confirmed downstream cancellation after a forwarded terminal was rejected")
	}
	good.StreamOutcome = "semantic_complete_downstream_cancelled"
	good.DownstreamContextState = "context_canceled"
	good.OutboundContextState = "context_canceled"
	good.Response.Body = terminal
	good.Response.Status = 200
	good.PublicRequest.URL = "https://api.githubcopilot.com/responses"
	if !liveCaptureStreamProven(good, "gpt-6-luna") {
		t.Fatal("captured confirmed downstream cancellation was rejected")
	}
	contradictory := good
	contradictory.CaptureTruncated = true
	if liveCaptureStreamProven(contradictory, "gpt-6-luna") {
		t.Fatal("contradictory truncated capture was accepted")
	}
	contradictory = good
	contradictory.StreamReadErrorKind = "context_deadline_exceeded"
	if liveCaptureStreamProven(contradictory, "gpt-6-luna") {
		t.Fatal("contradictory timeout capture was accepted")
	}
	contradictory = good
	contradictory.StreamTerminalForwarded = false
	if liveCaptureStreamProven(contradictory, "gpt-6-luna") {
		t.Fatal("contradictory unforwarded terminal was accepted")
	}
	for _, test := range []struct {
		name       string
		capture    liveServerToolCapture
		downstream error
		outbound   error
	}{
		{"early-cancel", liveServerToolCapture{ErrorClass: good.ErrorClass, StreamReadErrorKind: good.StreamReadErrorKind, StreamTerminalForwarded: true}, context.Canceled, context.Canceled},
		{"timeout", liveServerToolCapture{ErrorClass: good.ErrorClass, StreamReadErrorKind: "context_deadline_exceeded", StreamTerminalComplete: true, StreamTerminalForwarded: true}, context.DeadlineExceeded, context.DeadlineExceeded},
		{"truncated", liveServerToolCapture{ErrorClass: good.ErrorClass, StreamReadErrorKind: good.StreamReadErrorKind, StreamTerminalComplete: true, StreamTerminalForwarded: true, CaptureTruncated: true}, context.Canceled, context.Canceled},
		{"not-forwarded", liveServerToolCapture{ErrorClass: good.ErrorClass, StreamReadErrorKind: good.StreamReadErrorKind, StreamTerminalComplete: true}, context.Canceled, context.Canceled},
		{"downstream-active", good, nil, context.Canceled},
		{"outbound-active", good, context.Canceled, nil},
	} {
		if liveSemanticDownstreamCancellation(test.capture, test.downstream, test.outbound) {
			t.Fatalf("%s was accepted as a confirmed downstream cancellation", test.name)
		}
	}
}

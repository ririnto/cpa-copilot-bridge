//go:build !windows

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type liveCLIResult struct {
	exitCode      int
	timedOut      bool
	final         bool
	finalText     bool
	turnCompleted bool
	sourceURL     bool
	errorCode     string
	truncated     bool
	toolStarted   map[string]int
	toolCompleted map[string]int
	toolFailed    map[string]int
	toolIDs       map[string]string
	startedIDs    map[string]string
	completedIDs  map[string]string
	spawnedAgents map[string]bool
	doneAgents    map[string]bool
	eventTypes    map[string]int
}

type liveCLIBuffer struct {
	mu        sync.Mutex
	bytes     []byte
	truncated bool
}

func (b *liveCLIBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const maxBytes = 4 << 20
	available := maxBytes - len(b.bytes)
	if available > 0 {
		b.bytes = append(b.bytes, p[:min(len(p), available)]...)
	}
	if len(p) > available {
		b.truncated = true
	}
	return len(p), nil
}

func TestLiveCLIClients(t *testing.T) {
	if os.Getenv("CPA_LIVE_COPILOT_CLIENTS") != "1" {
		t.Skip("set CPA_LIVE_COPILOT_CLIENTS=1 to run real CLI clients")
	}
	binary := os.Getenv("CPA_BINARY")
	if binary == "" {
		t.Fatal("CPA_BINARY is required for live CLI clients")
	}
	token, err := readCopilotCLIKeychainToken()
	if err != nil {
		t.Fatal("Copilot CLI credential is unavailable")
	}
	login, userID, err := liveGitHubIdentity(token)
	if err != nil {
		t.Fatal("could not obtain authenticated Copilot identity")
	}
	storage, err := liveCopilotStorage(token, login, userID)
	if err != nil {
		t.Fatal("could not prepare temporary auth state")
	}
	profile := discoverLiveProfile(t, storage)
	base, stop := startLiveNativeHost(t, binary, profile.StorageJSON, profile.AuthMode, liveEndpointOverrides(profile.Catalog))
	defer stop()
	t.Logf("live client profile=%s model_count=%d", profile.AuthMode, len(profile.Catalog))
	for _, client := range []string{"claude", "codex"} {
		client := client
		t.Run(client, func(t *testing.T) {
			testLiveCLIClient(t, client, base)
		})
	}
}

func testLiveCLIClient(t *testing.T, client, base string) {
	t.Helper()
	path, err := exec.LookPath(client)
	if err != nil {
		t.Errorf("%s CLI is not installed", client)
		return
	}
	versionCommand := exec.Command(path, "--version")
	versionOutput, err := versionCommand.Output()
	if err != nil {
		t.Errorf("%s CLI version check failed", client)
		return
	}
	t.Logf("client=%s version=%s", client, strings.TrimSpace(string(versionOutput)))
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal("could not restrict temporary client home")
	}
	if client == "codex" {
		config := fmt.Sprintf("model = \"gpt-6-luna\"\nmodel_provider = \"task_proxy\"\nweb_search = \"live\"\n[features]\nmulti_agent_v2 = true\n[agents]\nmax_concurrent_threads_per_session = 2\n[model_providers.task_proxy]\nname = \"Task proxy\"\nbase_url = %q\nmodel_catalog_url = %q\nenv_key = \"TASK_PROXY_API_KEY\"\nwire_api = \"responses\"\nrequest_max_retries = 0\nstream_max_retries = 0\n", base+"/v1", base+"/v1/models")
		if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(config), 0600); err != nil {
			t.Fatal("could not write temporary Codex config")
		}
	}
	model := "claude-haiku-5.5"
	if client == "codex" {
		model = "gpt-6-luna"
	}
	t.Logf("client=%s model=%s", client, model)
	baseline := runLiveCLI(t, path, client, base, root, "Reply with the single word READY.")
	logLiveCLIResult(t, client, "baseline", baseline)
	baselineOK := baseline.exitCode == 0 && baseline.final && !baseline.truncated
	if !baselineOK {
		t.Log("task=web_search status=NOTRUN reason=baseline_failed")
		t.Log("task=subagent status=NOTRUN reason=baseline_failed")
		t.Errorf("%s baseline failed", client)
		return
	}
	search := runLiveCLI(t, path, client, base, root, "Use your web search tool to find NASA's official Moon facts page. Give one fact and its source URL. Actually perform the search before answering.")
	logLiveCLIResult(t, client, "web_search", search)
	if search.exitCode != 0 || !search.final || search.truncated || !liveSearchCompleted(client, search) {
		t.Errorf("%s web search did not complete", client)
	}
	delegation := runLiveCLI(t, path, client, base, root, "Delegate this bounded task to a subagent: calculate 17 multiplied by 19. Wait for the subagent to finish, then report its answer. Actually invoke the delegation tool.")
	logLiveCLIResult(t, client, "subagent", delegation)
	if delegation.exitCode != 0 || !delegation.final || delegation.truncated || !liveDelegationCompleted(client, delegation) {
		t.Errorf("%s subagent did not complete", client)
	}
}

func runLiveCLI(t *testing.T, path, client, base, root, prompt string) liveCLIResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var args []string
	if client == "claude" {
		args = []string{"--bare", "--print", "--output-format", "stream-json", "--verbose", "--no-session-persistence", "--permission-mode", "dontAsk", "--allowedTools", "WebSearch,Agent,Task", "--model", "claude-haiku-5.5", prompt}
	} else {
		args = []string{"exec", "--json", "--ephemeral", "--ignore-rules", "--skip-git-repo-check", "--sandbox", "read-only", "--model", "gpt-6-luna", "--cd", root, prompt}
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Dir = root
	command.Env = liveCLIEnvironment(client, base, root)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 5 * time.Second
	stdout := &liveCLIBuffer{}
	stderr := &liveCLIBuffer{}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	result := parseLiveCLIEvents(client, stdout.bytes)
	result.errorCode = safeCLIErrorCode(append(append([]byte(nil), stdout.bytes...), stderr.bytes...))
	result.truncated = stdout.truncated || stderr.truncated
	if command.ProcessState != nil {
		result.exitCode = command.ProcessState.ExitCode()
	} else {
		result.exitCode = -1
	}
	result.timedOut = ctx.Err() != nil
	if err != nil && result.exitCode == 0 {
		result.exitCode = -1
	}
	return result
}

func liveCLIEnvironment(client, base, root string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + root,
		"TMPDIR=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(root, "cache"),
		"XDG_DATA_HOME=" + filepath.Join(root, "data"),
		"NO_COLOR=1",
		"CI=1",
		"TERM=dumb",
	}
	if client == "claude" {
		env = append(env, "ANTHROPIC_BASE_URL="+base, "ANTHROPIC_API_KEY="+liveCopilotClientKey, "CLAUDE_CONFIG_DIR="+filepath.Join(root, "claude"), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	} else {
		env = append(env, "CODEX_HOME="+root, "TASK_PROXY_API_KEY="+liveCopilotClientKey)
	}
	return env
}

func parseLiveCLIEvents(client string, output []byte) liveCLIResult {
	result := liveCLIResult{toolStarted: map[string]int{}, toolCompleted: map[string]int{}, toolFailed: map[string]int{}, toolIDs: map[string]string{}, startedIDs: map[string]string{}, completedIDs: map[string]string{}, spawnedAgents: map[string]bool{}, doneAgents: map[string]bool{}, eventTypes: map[string]int{}}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		typeName, _ := event["type"].(string)
		result.eventTypes[typeName]++
		if client == "claude" {
			parseClaudeEvent(&result, event)
		} else {
			parseCodexEvent(&result, event)
		}
	}
	return result
}

func parseClaudeEvent(result *liveCLIResult, event map[string]any) {
	typeName, _ := event["type"].(string)
	if typeName == "result" {
		isError, _ := event["is_error"].(bool)
		text, _ := event["result"].(string)
		result.final = !isError && strings.TrimSpace(text) != ""
		result.sourceURL = result.sourceURL || strings.Contains(text, "https://")
	}
	message, _ := event["message"].(map[string]any)
	content, _ := message["content"].([]any)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		kind, _ := block["type"].(string)
		if kind == "tool_use" {
			name, _ := block["name"].(string)
			result.toolStarted[name]++
			if id, ok := block["id"].(string); ok {
				result.toolIDs[id] = name
			}
		}
		if kind == "tool_result" {
			name := "tool_result"
			if toolUseID, ok := block["tool_use_id"].(string); ok {
				if toolName, found := result.toolIDs[toolUseID]; found {
					name = toolName
				}
			}
			isError, _ := block["is_error"].(bool)
			if isError {
				result.toolFailed[name]++
			} else {
				result.toolCompleted[name]++
			}
		}
	}
}

func parseCodexEvent(result *liveCLIResult, event map[string]any) {
	typeName, _ := event["type"].(string)
	if typeName == "turn.completed" {
		result.turnCompleted = true
	}
	item, _ := event["item"].(map[string]any)
	if item == nil {
		result.final = result.turnCompleted && result.finalText
		return
	}
	name, _ := item["type"].(string)
	if name == "agent_message" && typeName == "item.completed" {
		text, _ := item["text"].(string)
		result.finalText = result.finalText || strings.TrimSpace(text) != ""
		result.sourceURL = result.sourceURL || strings.Contains(text, "https://")
	}
	if name == "collab_tool_call" {
		tool, _ := item["tool"].(string)
		name += ":" + tool
	}
	id, _ := item["id"].(string)
	if typeName == "item.started" {
		result.toolStarted[name]++
		if id != "" {
			result.startedIDs[id] = name
		}
	}
	if typeName == "item.completed" {
		status, _ := item["status"].(string)
		if id != "" && result.startedIDs[id] == name && status != "failed" {
			result.toolCompleted[name]++
			result.completedIDs[id] = name
			if name == "collab_tool_call:spawn_agent" && status == "completed" {
				if receiverIDs, ok := item["receiver_thread_ids"].([]any); ok {
					for _, raw := range receiverIDs {
						if agentID, ok := raw.(string); ok {
							result.spawnedAgents[agentID] = true
						}
					}
				}
			}
			if name == "collab_tool_call:wait" && status == "completed" {
				states, _ := item["agents_states"].(map[string]any)
				for agentID, raw := range states {
					state, _ := raw.(map[string]any)
					if state["status"] == "completed" {
						result.doneAgents[agentID] = true
					}
				}
			}
		}
	}
	if typeName == "item.failed" {
		result.toolFailed[name]++
	}
	result.final = result.turnCompleted && result.finalText
}

func liveSearchCompleted(client string, result liveCLIResult) bool {
	name := "WebSearch"
	if client == "codex" {
		name = "web_search"
	}
	return result.toolStarted[name] > 0 && result.toolCompleted[name] > 0 && result.sourceURL
}

func TestLiveCLIEventEvidence(t *testing.T) {
	ordinary := parseLiveCLIEvents("codex", []byte(`{"type":"item.started","item":{"id":"m1","type":"agent_message"}}
{"type":"item.completed","item":{"id":"m1","type":"agent_message","text":"I searched and delegated. https://example.com"}}
{"type":"turn.completed"}`))
	if !ordinary.final || liveSearchCompleted("codex", ordinary) || liveDelegationCompleted("codex", ordinary) {
		t.Fatal("ordinary assistant text was counted as a completed tool")
	}
	webStarted := parseLiveCLIEvents("codex", []byte(`{"type":"item.started","item":{"id":"w1","type":"web_search"}}
{"type":"item.completed","item":{"id":"w2","type":"web_search","query":"moon","action":{"type":"search","query":"moon"}}}
{"type":"item.completed","item":{"id":"m1","type":"agent_message","text":"https://example.com"}}
{"type":"turn.completed"}`))
	if liveSearchCompleted("codex", webStarted) {
		t.Fatal("mismatched web search item IDs were counted as completion")
	}
	webCompleted := parseLiveCLIEvents("codex", []byte(`{"type":"item.started","item":{"id":"w1","type":"web_search"}}
{"type":"item.completed","item":{"id":"w1","type":"web_search","query":"moon","action":{"type":"search","query":"moon"}}}
{"type":"item.completed","item":{"id":"m1","type":"agent_message","text":"https://example.com"}}
{"type":"turn.completed"}`))
	if !liveSearchCompleted("codex", webCompleted) {
		t.Fatal("completed web search with a source URL was not counted")
	}
	spawnOnly := parseLiveCLIEvents("codex", []byte(`{"type":"item.started","item":{"id":"s1","type":"collab_tool_call","tool":"spawn_agent","status":"in_progress"}}
{"type":"item.completed","item":{"id":"s1","type":"collab_tool_call","tool":"spawn_agent","status":"completed","receiver_thread_ids":["child"]}}
{"type":"item.completed","item":{"id":"m1","type":"agent_message","text":"done"}}
{"type":"turn.completed"}`))
	if liveDelegationCompleted("codex", spawnOnly) {
		t.Fatal("spawn alone was counted as completed delegation")
	}
	withWait := parseLiveCLIEvents("codex", []byte(`{"type":"item.started","item":{"id":"s1","type":"collab_tool_call","tool":"spawn_agent","status":"in_progress"}}
{"type":"item.completed","item":{"id":"s1","type":"collab_tool_call","tool":"spawn_agent","status":"completed","receiver_thread_ids":["child"]}}
{"type":"item.started","item":{"id":"wait1","type":"collab_tool_call","tool":"wait","status":"in_progress"}}
{"type":"item.completed","item":{"id":"wait1","type":"collab_tool_call","tool":"wait","status":"completed","agents_states":{"child":{"status":"completed"}}}}
{"type":"item.completed","item":{"id":"m1","type":"agent_message","text":"323"}}
{"type":"turn.completed"}`))
	if !liveDelegationCompleted("codex", withWait) {
		t.Fatal("completed child observed through wait was not counted")
	}
	claudeTool := parseLiveCLIEvents("claude", []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool1","name":"Task"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool1","is_error":false}]}}
{"type":"result","is_error":false,"result":"323"}`))
	if !claudeTool.final || !liveDelegationCompleted("claude", claudeTool) || liveSearchCompleted("claude", claudeTool) {
		t.Fatal("Claude tool use/result was not identified precisely")
	}
	claudeError := parseLiveCLIEvents("claude", []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool1","name":"WebSearch"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool1","is_error":true}]}}
{"type":"result","is_error":false,"result":"https://example.com"}`))
	if liveSearchCompleted("claude", claudeError) {
		t.Fatal("failed Claude tool result was counted as successful search")
	}
}

func liveDelegationCompleted(client string, result liveCLIResult) bool {
	if client == "claude" {
		return (result.toolStarted["Agent"] > 0 && result.toolCompleted["Agent"] > 0) || (result.toolStarted["Task"] > 0 && result.toolCompleted["Task"] > 0)
	}
	if result.toolCompleted["collab_tool_call:spawn_agent"] == 0 || result.toolCompleted["collab_tool_call:wait"] == 0 {
		return false
	}
	for agentID := range result.spawnedAgents {
		if result.doneAgents[agentID] {
			return true
		}
	}
	return false
}

func logLiveCLIResult(t *testing.T, client, task string, result liveCLIResult) {
	t.Helper()
	status := "PASS"
	if result.exitCode != 0 || !result.final || result.truncated {
		status = "FAIL"
	}
	if task == "web_search" && !liveSearchCompleted(client, result) {
		status = "FAIL"
	}
	if task == "subagent" && !liveDelegationCompleted(client, result) {
		status = "FAIL"
	}
	t.Logf("task=%s status=%s exit=%d timeout=%t final=%t truncated=%t error_code=%s events=%v tool_started=%v tool_completed=%v tool_failed=%v", task, status, result.exitCode, result.timedOut, result.final, result.truncated, result.errorCode, result.eventTypes, result.toolStarted, result.toolCompleted, result.toolFailed)
}

func safeCLIErrorCode(output []byte) string {
	if match := regexp.MustCompile(`(?i)\b(?:HTTP[/ ]+|status(?: code)?[/ :=]+)([45][0-9][0-9])\b`).FindSubmatch(output); len(match) == 2 {
		return "http_" + string(match[1])
	}
	for _, code := range []string{"model_not_found", "invalid_request_error", "rate_limit_error", "authentication_error", "permission_error"} {
		if bytes.Contains(bytes.ToLower(output), []byte(code)) {
			return code
		}
	}
	return "unclassified"
}

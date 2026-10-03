package translate

import (
	"encoding/json"
	"fmt"
	"strings"
)

type responsesClaudeStreamState struct {
	Started   bool
	Stopped   bool
	MessageID string
	Model     string
	NextBlock int
	Blocks    map[string]*responsesClaudeBlock
}

type responsesClaudeBlock struct {
	Index           int
	Kind            string
	Open            bool
	SawDelta        bool
	Accumulated     string
	TextAccumulated string
	SignatureSent   bool
	RedactedSent    bool
	Encrypted       string
	FunctionID      string
	FunctionItemID  string
	FunctionCallID  string
	FunctionName    string
}

func responsesStreamToClaude(model string, frame []byte, state *any) ([][]byte, error) {
	if state == nil {
		return nil, fmt.Errorf("Responses-to-Claude stream translation requires state")
	}
	streamState, ok := (*state).(*responsesClaudeStreamState)
	if !ok {
		streamState = &responsesClaudeStreamState{
			Model:  model,
			Blocks: make(map[string]*responsesClaudeBlock),
		}
		*state = streamState
	}
	event, data, done, err := parseSSEFrame(frame)
	if err != nil {
		return nil, err
	}
	if done {
		if streamState.Stopped {
			return nil, nil
		}
		return nil, fmt.Errorf("Responses stream ended before a terminal response event")
	}
	if len(data) == 0 {
		return nil, nil
	}
	payload, err := decodeObject(data)
	if err != nil {
		return nil, fmt.Errorf("decode Responses SSE event %q: %w", event, err)
	}
	if event == "" {
		event = stringValue(payload["type"])
	}

	switch event {
	case "error", "response.failed":
		errorObject := objectValue(payload["error"])
		if response, okResponse := payload["response"].(map[string]any); okResponse {
			if nested := objectValue(response["error"]); len(nested) > 0 {
				errorObject = nested
			}
		}
		message := firstNonEmptyString(stringValue(errorObject["message"]), stringValue(payload["message"]), "unknown upstream stream error")
		return nil, fmt.Errorf("Copilot Responses stream failed: %s", message)
	}

	var out [][]byte
	responseObject := objectValue(payload["response"])
	if !streamState.Started && (event == "response.created" || event == "response.in_progress" || len(responseObject) > 0) {
		out = append(out, streamState.start(responseObject, payload)...)
	}
	if !streamState.Started && strings.HasPrefix(event, "response.") {
		out = append(out, streamState.start(responseObject, payload)...)
	}

	switch event {
	case "response.output_item.added":
		item := objectValue(payload["item"])
		key := itemKey(payload)
		switch stringValue(item["type"]) {
		case "reasoning":
			block := streamState.block(key, "thinking")
			block.Encrypted = rawStringValue(item["encrypted_content"])
			out = append(out, streamState.openThinking(block)...)
		case "function_call", "custom_tool_call":
			block := streamState.block(key, "tool_use")
			setResponsesFunction(block, item)
			block.FunctionName = stringValue(item["name"])
			out = append(out, streamState.openTool(block)...)
		}
	case "response.content_part.added":
		part := objectValue(payload["part"])
		switch stringValue(part["type"]) {
		case "output_text", "text", "refusal":
			block := streamState.block(contentKey(payload), "text")
			out = append(out, streamState.openText(block)...)
			if text := firstRawString(part, "text", "refusal"); text != "" {
				out = append(out, streamState.delta(block, "text_delta", "text", text)...)
			}
		}
	case "response.output_text.delta", "response.refusal.delta":
		block := streamState.block(contentKey(payload), "text")
		out = append(out, streamState.openText(block)...)
		if delta := rawStringValue(payload["delta"]); delta != "" {
			out = append(out, streamState.delta(block, "text_delta", "text", delta)...)
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		block := streamState.block(itemKey(payload), "thinking")
		out = append(out, streamState.openThinking(block)...)
		if delta := rawStringValue(payload["delta"]); delta != "" {
			out = append(out, streamState.delta(block, "thinking_delta", "thinking", delta)...)
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		block := streamState.block(itemKey(payload), "tool_use")
		if block.FunctionID == "" {
			block.FunctionID = firstNonEmptyString(rawStringValue(payload["call_id"]), rawStringValue(payload["item_id"]))
		}
		if block.FunctionName == "" {
			block.FunctionName = stringValue(payload["name"])
		}
		out = append(out, streamState.openTool(block)...)
		if delta := rawStringValue(payload["delta"]); delta != "" {
			out = append(out, streamState.delta(block, "input_json_delta", "partial_json", delta)...)
		}
	case "response.content_part.done":
		out = append(out, streamState.closeBlock(contentKey(payload))...)
	case "response.output_item.done":
		item := objectValue(payload["item"])
		key := itemKey(payload)
		block := streamState.Blocks[key]
		if block == nil {
			switch stringValue(item["type"]) {
			case "reasoning":
				block = streamState.block(key, "thinking")
				block.Encrypted = rawStringValue(item["encrypted_content"])
				out = append(out, streamState.openThinking(block)...)
			case "function_call", "custom_tool_call":
				block = streamState.block(key, "tool_use")
			}
		}
		if block != nil {
			if block.Kind == "thinking" {
				out = append(out, streamState.openThinking(block)...)
			}
			if block.Kind == "tool_use" {
				setResponsesFunction(block, item)
				block.FunctionName = stringValue(item["name"])
				out = append(out, streamState.openTool(block)...)
			}
			switch block.Kind {
			case "thinking":
				fullText := responsesReasoningText(item)
				if !block.SawDelta && fullText != "" {
					out = append(out, streamState.delta(block, "thinking_delta", "thinking", fullText)...)
				}
				if encrypted := rawStringValue(item["encrypted_content"]); encrypted != "" {
					block.Encrypted = encrypted
				}
				if block.Encrypted != "" && !strings.HasPrefix(block.Encrypted, redactedThinkingPrefix) {
					if !block.SignatureSent {
						out = append(out, streamState.delta(block, "signature_delta", "signature", block.Encrypted)...)
					}
				}
			case "tool_use":
				arguments := firstRawString(item, "arguments", "input")
				if !block.SawDelta && arguments != "" {
					out = append(out, streamState.delta(block, "input_json_delta", "partial_json", arguments)...)
				}
			}
			out = append(out, streamState.closeBlock(key)...)
		}
	case "response.completed", "response.incomplete":
		if event == "response.completed" {
			if errFailure := responsesFailure(responseObject); errFailure != nil {
				return nil, errFailure
			}
		}
		out = append(out, streamState.reconcileTerminalOutput(responseObject)...)
		out = append(out, streamState.finish(responseObject)...)
	}
	return out, nil
}

func (s *responsesClaudeStreamState) start(response, payload map[string]any) [][]byte {
	if s.Started {
		return nil
	}
	s.Started = true
	s.MessageID = firstNonEmptyString(stringValue(response["id"]), stringValue(payload["response_id"]), "msg_copilot")
	s.Model = firstNonEmptyString(stringValue(response["model"]), s.Model)
	usage := objectValue(response["usage"])
	message := map[string]any{
		"id":            s.MessageID,
		"type":          "message",
		"role":          "assistant",
		"model":         s.Model,
		"content":       []any{},
		"stop_reason":   nil,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  numberValue(usage["input_tokens"]),
			"output_tokens": 0,
		},
	}
	if cached, ok := objectValue(usage["input_tokens_details"])["cached_tokens"]; ok {
		message["usage"].(map[string]any)["cache_read_input_tokens"] = numberValue(cached)
	}
	return [][]byte{claudeSSE("message_start", map[string]any{"type": "message_start", "message": message})}
}

func (s *responsesClaudeStreamState) block(key, kind string) *responsesClaudeBlock {
	if key == "" {
		key = fmt.Sprintf("%s:%d", kind, len(s.Blocks))
	}
	if block := s.Blocks[key]; block != nil {
		return block
	}
	block := &responsesClaudeBlock{Index: s.NextBlock, Kind: kind}
	s.NextBlock++
	s.Blocks[key] = block
	return block
}

func (s *responsesClaudeStreamState) openText(block *responsesClaudeBlock) [][]byte {
	if block.Open {
		return nil
	}
	block.Open = true
	return [][]byte{claudeSSE("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         block.Index,
		"content_block": map[string]any{"type": "text", "text": ""},
	})}
}

func (s *responsesClaudeStreamState) openThinking(block *responsesClaudeBlock) [][]byte {
	if block.Open {
		return nil
	}
	block.Open = true
	contentBlock := map[string]any{"type": "thinking", "thinking": ""}
	if strings.HasPrefix(block.Encrypted, redactedThinkingPrefix) {
		block.RedactedSent = true
		contentBlock = map[string]any{
			"type": "redacted_thinking",
			"data": strings.TrimPrefix(block.Encrypted, redactedThinkingPrefix),
		}
	}
	return [][]byte{claudeSSE("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         block.Index,
		"content_block": contentBlock,
	})}
}

func (s *responsesClaudeStreamState) openTool(block *responsesClaudeBlock) [][]byte {
	if block.Open {
		return nil
	}
	block.Open = true
	return [][]byte{claudeSSE("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": block.Index,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    block.FunctionID,
			"name":  block.FunctionName,
			"input": map[string]any{},
		},
	})}
}

func (s *responsesClaudeStreamState) delta(block *responsesClaudeBlock, deltaType, field, value string) [][]byte {
	if value == "" {
		return nil
	}
	block.SawDelta = true
	block.Accumulated += value
	if deltaType == "text_delta" || deltaType == "thinking_delta" {
		block.TextAccumulated += value
	}
	if deltaType == "signature_delta" {
		block.SignatureSent = true
	}
	return [][]byte{claudeSSE("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": block.Index,
		"delta": map[string]any{"type": deltaType, field: value},
	})}
}

func (s *responsesClaudeStreamState) closeBlock(key string) [][]byte {
	block := s.Blocks[key]
	if block == nil || !block.Open {
		return nil
	}
	block.Open = false
	return [][]byte{claudeSSE("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": block.Index,
	})}
}

func (s *responsesClaudeStreamState) reconcileTerminalOutput(response map[string]any) [][]byte {
	if s.Stopped {
		return nil
	}
	var out [][]byte
	for outputIndex, rawItem := range arrayValue(response["output"]) {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		key := fmt.Sprintf("item:%d", outputIndex)
		switch stringValue(item["type"]) {
		case "message":
			for contentIndex, rawPart := range arrayValue(item["content"]) {
				part, okPart := rawPart.(map[string]any)
				if !okPart {
					continue
				}
				if stringValue(part["type"]) != "output_text" && stringValue(part["type"]) != "text" && stringValue(part["type"]) != "refusal" {
					continue
				}
				text := firstRawString(part, "text", "refusal")
				contentKey := fmt.Sprintf("%s:content:%d", key, contentIndex)
				out = append(out, s.reconcileText(contentKey, text)...)
			}
		case "reasoning":
			out = append(out, s.reconcileReasoning(key, item)...)
		case "function_call", "custom_tool_call":
			out = append(out, s.reconcileFunction(key, item)...)
		}
	}
	return out
}

func (s *responsesClaudeStreamState) reconcileText(key, text string) [][]byte {
	block := s.Blocks[key]
	remainder := text
	if block != nil && block.SawDelta {
		if !strings.HasPrefix(text, block.TextAccumulated) {
			return nil
		}
		remainder = strings.TrimPrefix(text, block.TextAccumulated)
	}
	if remainder == "" {
		return nil
	}
	target, targetKey := terminalBlock(s, key, "text", block)
	out := s.openText(target)
	out = append(out, s.delta(target, "text_delta", "text", remainder)...)
	out = append(out, s.closeBlock(targetKey)...)
	return out
}

func (s *responsesClaudeStreamState) reconcileReasoning(key string, item map[string]any) [][]byte {
	block := s.Blocks[key]
	fullText := responsesReasoningText(item)
	textRemainder := fullText
	if block != nil && block.TextAccumulated != "" {
		if !strings.HasPrefix(fullText, block.TextAccumulated) {
			textRemainder = ""
		} else {
			textRemainder = strings.TrimPrefix(fullText, block.TextAccumulated)
		}
	}
	encrypted := rawStringValue(item["encrypted_content"])
	redacted := strings.HasPrefix(encrypted, redactedThinkingPrefix)
	needsRedacted := redacted && (block == nil || !block.RedactedSent)
	needsSignature := encrypted != "" && !redacted && (block == nil || !block.SignatureSent)
	if textRemainder == "" && !needsRedacted && !needsSignature {
		return nil
	}
	target, targetKey := terminalBlock(s, key, "thinking", block)
	if redacted && needsRedacted && block != nil && block.Open && !block.RedactedSent {
		targetKey = key + ":terminal"
		target = s.block(targetKey, "thinking")
	}
	if redacted && !target.RedactedSent {
		target.Encrypted = encrypted
	}
	out := s.openThinking(target)
	out = append(out, s.delta(target, "thinking_delta", "thinking", textRemainder)...)
	if needsSignature && !target.SignatureSent {
		out = append(out, s.delta(target, "signature_delta", "signature", encrypted)...)
	}
	out = append(out, s.closeBlock(targetKey)...)
	return out
}

func (s *responsesClaudeStreamState) reconcileFunction(key string, item map[string]any) [][]byte {
	block := s.Blocks[key]
	arguments := responsesFunctionArguments(item)
	argumentRemainder := arguments
	if block != nil && block.SawDelta {
		if !strings.HasPrefix(arguments, block.Accumulated) {
			argumentRemainder = ""
		} else {
			argumentRemainder = strings.TrimPrefix(arguments, block.Accumulated)
		}
	}
	if block != nil && argumentRemainder == "" {
		return nil
	}
	target, targetKey := terminalBlock(s, key, "tool_use", block)
	setResponsesFunction(target, item)
	target.FunctionName = stringValue(item["name"])
	out := s.openTool(target)
	out = append(out, s.delta(target, "input_json_delta", "partial_json", argumentRemainder)...)
	out = append(out, s.closeBlock(targetKey)...)
	return out
}

func terminalBlock(s *responsesClaudeStreamState, key, kind string, current *responsesClaudeBlock) (*responsesClaudeBlock, string) {
	if current == nil {
		return s.block(key, kind), key
	}
	if current.Open {
		return current, key
	}
	terminalKey := key + ":terminal"
	return s.block(terminalKey, kind), terminalKey
}

func setResponsesFunction(block *responsesClaudeBlock, item map[string]any) {
	itemID := rawStringValue(item["id"])
	callID := rawStringValue(item["call_id"])
	block.FunctionItemID = itemID
	block.FunctionCallID = callID
	block.FunctionID = encodeClaudeToolID(itemID, callID)
}

func responsesFunctionArguments(item map[string]any) string {
	if arguments := rawStringValue(item["arguments"]); arguments != "" {
		return arguments
	}
	if input, ok := item["input"]; ok {
		encoded, err := json.Marshal(input)
		if err == nil {
			return string(encoded)
		}
	}
	return ""
}

func (s *responsesClaudeStreamState) finish(response map[string]any) [][]byte {
	if s.Stopped {
		return nil
	}
	var out [][]byte
	hasToolUse := false
	for key, block := range s.Blocks {
		if block.Kind == "tool_use" {
			hasToolUse = true
		}
		out = append(out, s.closeBlock(key)...)
	}
	usage := objectValue(response["usage"])
	finalUsage := map[string]any{"output_tokens": numberValue(usage["output_tokens"])}
	if inputTokens, ok := usage["input_tokens"]; ok {
		finalUsage["input_tokens"] = numberValue(inputTokens)
	}
	if cachedTokens, ok := objectValue(usage["input_tokens_details"])["cached_tokens"]; ok {
		finalUsage["cache_read_input_tokens"] = numberValue(cachedTokens)
	}
	out = append(out, claudeSSE("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   responsesStopReason(response, hasToolUse),
			"stop_sequence": nil,
		},
		"usage": finalUsage,
	}))
	out = append(out, claudeSSE("message_stop", map[string]any{"type": "message_stop"}))
	s.Stopped = true
	return out
}

func parseSSEFrame(frame []byte) (event string, data []byte, done bool, err error) {
	normalized := strings.ReplaceAll(string(frame), "\r\n", "\n")
	var dataLines []string
	for _, line := range strings.Split(normalized, "\n") {
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(dataLines) == 0 {
		return event, nil, false, nil
	}
	joined := strings.Join(dataLines, "\n")
	if joined == "[DONE]" {
		return event, nil, true, nil
	}
	if !json.Valid([]byte(joined)) {
		return "", nil, false, fmt.Errorf("Responses SSE data is invalid JSON")
	}
	return event, []byte(joined), false, nil
}

func claudeSSE(event string, payload map[string]any) []byte {
	data, _ := json.Marshal(payload)
	return []byte("event: " + event + "\ndata: " + string(data) + "\n\n")
}

func itemKey(payload map[string]any) string {
	if index := numberKey(payload["output_index"]); index != "" {
		return "item:" + index
	}
	return "item:" + firstNonEmptyString(stringValue(payload["item_id"]), stringValue(objectValue(payload["item"])["id"]))
}

func contentKey(payload map[string]any) string {
	return itemKey(payload) + ":content:" + firstNonEmptyString(numberKey(payload["content_index"]), "0")
}

func numberKey(value any) string {
	switch number := value.(type) {
	case json.Number:
		return number.String()
	case float64:
		return fmt.Sprintf("%.0f", number)
	case int:
		return fmt.Sprintf("%d", number)
	case int64:
		return fmt.Sprintf("%d", number)
	default:
		return ""
	}
}

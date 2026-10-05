package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func chatRequestToResponses(model string, body []byte, stream bool) ([]byte, error) {
	if err := validateChatRequestForResponses(body); err != nil {
		return nil, err
	}
	prepared, err := chatRequestToClaudeToolIDs(body)
	if err != nil {
		return nil, err
	}
	claude := registry.TranslateRequest(sdktranslator.FormatOpenAI, sdktranslator.FormatClaude, model, prepared, stream)
	if len(claude) == 0 || !json.Valid(claude) {
		return nil, fmt.Errorf("official Chat-to-Claude request translation failed")
	}
	responses, err := claudeRequestToResponses(model, claude, stream)
	if err != nil {
		return nil, err
	}
	if key := gjson.GetBytes(body, "prompt_cache_key"); key.Exists() {
		responses, err = sjson.SetRawBytes(responses, "prompt_cache_key", []byte(key.Raw))
		if err != nil {
			return nil, fmt.Errorf("preserve Chat prompt_cache_key")
		}
	}
	if effort := gjson.GetBytes(body, "reasoning_effort"); effort.Exists() {
		if effort.Type != gjson.String {
			return nil, fmt.Errorf("Chat reasoning_effort must be a string")
		}
		switch effort.String() {
		case "none":
			responses, err = sjson.DeleteBytes(responses, "reasoning")
		default:
			responses, err = sjson.SetBytes(responses, "reasoning.effort", effort.String())
		}
		if err != nil {
			return nil, fmt.Errorf("preserve Chat reasoning_effort")
		}
	}
	return restoreChatOpaqueToResponses(responses, body, model)
}

func chatRequestToClaude(model string, body []byte, stream bool) ([]byte, error) {
	if !json.Valid(body) {
		return nil, fmt.Errorf("request body is not valid JSON")
	}
	prepared, err := chatRequestWithOpaqueAssistantMarkers(body)
	if err != nil {
		return nil, err
	}
	prepared, err = chatRequestToClaudeMessagesToolIDs(prepared)
	if err != nil {
		return nil, err
	}
	translated := registry.TranslateRequest(sdktranslator.FormatOpenAI, sdktranslator.FormatClaude, model, prepared, stream)
	if len(translated) == 0 || !json.Valid(translated) {
		return nil, fmt.Errorf("official Chat-to-Claude request translation failed")
	}
	translated, err = restoreClaudeMessagesToolIDs(translated)
	if err != nil {
		return nil, err
	}
	return restoreChatOpaqueToClaude(translated, body, model)
}

func chatRequestToClaudeMessagesToolIDs(body []byte) ([]byte, error) {
	if !json.Valid(body) {
		return nil, fmt.Errorf("Chat request is not valid JSON")
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body, nil
	}
	updated := body
	calls := make(map[string]string)
	callIDs := make(map[string]struct{})
	for messageIndex, message := range messages.Array() {
		if message.Get("role").String() == "assistant" {
			for callIndex, call := range message.Get("tool_calls").Array() {
				visibleID := call.Get("id").String()
				if visibleID != "" {
					_, callID, encoded, err := parseClaudeToolID(visibleID)
					if err != nil {
						return nil, fmt.Errorf("Chat tool call ID has an invalid Messages identity")
					}
					if !encoded {
						callID = visibleID
					}
					if callID == "" {
						return nil, fmt.Errorf("Chat tool call ID has no Messages identity")
					}
					if _, exists := calls[visibleID]; exists {
						return nil, fmt.Errorf("Chat tool call IDs are ambiguous for Messages translation")
					}
					if _, exists := callIDs[callID]; exists {
						return nil, fmt.Errorf("Chat Messages tool IDs are ambiguous")
					}
					carrier := encodeClaudeToolID("", callID)
					if carrier == "" {
						return nil, fmt.Errorf("Chat tool call ID cannot be represented in Messages")
					}
					calls[visibleID] = carrier
					callIDs[callID] = struct{}{}
					updated, err = sjson.SetBytes(updated, fmt.Sprintf("messages.%d.tool_calls.%d.id", messageIndex, callIndex), carrier)
					if err != nil {
						return nil, fmt.Errorf("encode Chat tool call ID for Messages translation")
					}
				}
			}
		}
	}
	for messageIndex, message := range messages.Array() {
		if message.Get("role").String() == "tool" {
			visibleID := message.Get("tool_call_id").String()
			if visibleID != "" {
				carrier, exists := calls[visibleID]
				if !exists {
					_, decodedCallID, encoded, err := parseClaudeToolID(visibleID)
					if err != nil {
						return nil, fmt.Errorf("Chat tool result ID has an invalid Messages identity")
					}
					if encoded {
						if decodedCallID == "" {
							return nil, fmt.Errorf("Chat tool result ID has no Messages identity")
						}
						carrier = encodeClaudeToolID("", decodedCallID)
					} else {
						carrier = encodeClaudeToolID("", visibleID)
					}
				}
				if carrier == "" {
					return nil, fmt.Errorf("Chat tool result ID cannot be represented in Messages")
				}
				if carrier != visibleID {
					var err error
					updated, err = sjson.SetBytes(updated, fmt.Sprintf("messages.%d.tool_call_id", messageIndex), carrier)
					if err != nil {
						return nil, fmt.Errorf("encode Chat tool result ID for Messages translation")
					}
				}
			}
		}
	}
	return updated, nil
}

func restoreClaudeMessagesToolIDs(body []byte) ([]byte, error) {
	if !json.Valid(body) {
		return nil, fmt.Errorf("translated Messages request is not valid JSON")
	}
	updated := body
	for messageIndex, message := range gjson.GetBytes(body, "messages").Array() {
		for blockIndex, block := range message.Get("content").Array() {
			var path string
			var visibleID string
			switch block.Get("type").String() {
			case "tool_use":
				path = fmt.Sprintf("messages.%d.content.%d.id", messageIndex, blockIndex)
				visibleID = block.Get("id").String()
			case "tool_result":
				path = fmt.Sprintf("messages.%d.content.%d.tool_use_id", messageIndex, blockIndex)
				visibleID = block.Get("tool_use_id").String()
			}
			if path != "" && visibleID != "" {
				_, callID, encoded, err := parseClaudeToolID(visibleID)
				if err != nil {
					return nil, fmt.Errorf("translated Messages tool ID is invalid")
				}
				if encoded && callID == "" {
					return nil, fmt.Errorf("translated Messages tool ID has no original identity")
				}
				if encoded {
					updated, err = sjson.SetBytes(updated, path, callID)
					if err != nil {
						return nil, fmt.Errorf("restore original Messages tool ID")
					}
				}
			}
		}
	}
	return updated, nil
}

func chatRequestWithOpaqueAssistantMarkers(body []byte) ([]byte, error) {
	updated := body
	for index, message := range gjson.GetBytes(body, "messages").Array() {
		field := message.Get("reasoning_opaque")
		if message.Get("role").String() == "assistant" && field.Exists() && field.Type != gjson.Null && meaningfulOpaqueJSON([]byte(field.Raw)) {
			marker := chatOpaqueAssistantMarker(body, index)
			content, err := chatContentWithOpaqueMarker(message.Get("content"), marker)
			if err != nil {
				return nil, err
			}
			updated, err = sjson.SetRawBytes(updated, fmt.Sprintf("messages.%d.content", index), content)
			if err != nil {
				return nil, fmt.Errorf("preserve Chat assistant reasoning turn")
			}
		}
	}
	return updated, nil
}

func chatContentWithOpaqueMarker(content gjson.Result, marker string) ([]byte, error) {
	textBlock := []byte(`{"type":"text","text":""}`)
	textBlock, _ = sjson.SetBytes(textBlock, "text", marker)
	parts := [][]byte{textBlock}
	if content.Type == gjson.String && content.String() != "" {
		textBlock = []byte(`{"type":"text","text":""}`)
		textBlock, _ = sjson.SetBytes(textBlock, "text", content.String())
		parts = append(parts, textBlock)
	} else if content.IsArray() {
		for _, part := range content.Array() {
			parts = append(parts, []byte(part.Raw))
		}
	} else if content.Exists() && content.Type != gjson.Null && !(content.Type == gjson.String && content.String() == "") {
		return nil, fmt.Errorf("Chat assistant content cannot carry opaque reasoning safely")
	}
	return rawJSONArray(parts), nil
}

func chatOpaqueAssistantMarker(body []byte, messageIndex int) string {
	return fmt.Sprintf("cpa-copilot-reasoning-turn-%d-%s", messageIndex, digest(body))
}

func restoreChatOpaqueToClaude(claudeRequest, chatRequest []byte, model string) ([]byte, error) {
	chatMessages := gjson.GetBytes(chatRequest, "messages")
	if !chatMessages.IsArray() || !gjson.GetBytes(claudeRequest, "messages").IsArray() {
		return claudeRequest, nil
	}
	blocksByMarker := make(map[string][][]byte)
	for messageIndex, message := range chatMessages.Array() {
		field := message.Get("reasoning_opaque")
		if message.Get("role").String() == "assistant" && field.Exists() && field.Type != gjson.Null && meaningfulOpaqueJSON([]byte(field.Raw)) {
			raw := []byte(field.Raw)
			if field.Type == gjson.String {
				if strings.HasPrefix(field.String(), copilotOpaquePrefix) {
					value, err := decodeCopilotOpaque(field.String(), model)
					if err != nil {
						return nil, err
					}
					if err := validateChatAssistantOpaqueAnchor(chatMessageBytes(chatMessages.Array()), messageIndex, value.Anchor); err != nil {
						return nil, err
					}
					raw = value.Raw
				} else {
					raw = []byte(field.String())
				}
			}
			blocks, err := claudeThinkingBlocksFromOpaque(raw)
			if err != nil {
				return nil, err
			}
			blocksByMarker[chatOpaqueAssistantMarker(chatRequest, messageIndex)] = blocks
		}
	}
	found := make(map[string]bool, len(blocksByMarker))
	updated := claudeRequest
	for messageIndex, message := range gjson.GetBytes(claudeRequest, "messages").Array() {
		if message.Get("role").String() == "assistant" {
			content := message.Get("content")
			if content.IsArray() {
				blocks := make([][]byte, 0, len(content.Array()))
				changed := false
				for _, block := range content.Array() {
					marker := block.Get("text").String()
					if block.Get("type").String() == "text" {
						if reasoning, exists := blocksByMarker[marker]; exists {
							if found[marker] {
								return nil, fmt.Errorf("Chat opaque reasoning marker is ambiguous in Claude Messages history")
							}
							blocks = append(blocks, reasoning...)
							found[marker] = true
							changed = true
						} else {
							blocks = append(blocks, []byte(block.Raw))
						}
					} else {
						blocks = append(blocks, []byte(block.Raw))
					}
				}
				if changed {
					var err error
					updated, err = setRawArray(updated, fmt.Sprintf("messages.%d.content", messageIndex), blocks)
					if err != nil {
						return nil, err
					}
				}
			}
		}
	}
	for marker := range blocksByMarker {
		if !found[marker] {
			return nil, fmt.Errorf("Chat opaque reasoning has no Claude Messages history position")
		}
	}
	return updated, nil
}

func claudeThinkingBlocksFromOpaque(raw []byte) ([][]byte, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("Chat reasoning_opaque is not valid JSON")
	}
	value := gjson.ParseBytes(raw)
	items := value.Array()
	if value.IsObject() {
		items = []gjson.Result{value}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("Chat reasoning_opaque contains no reasoning blocks")
	}
	blocks := make([][]byte, 0, len(items))
	for _, item := range items {
		switch item.Get("type").String() {
		case "thinking", "redacted_thinking":
			blocks = append(blocks, []byte(item.Raw))
		case "reasoning":
			encrypted := item.Get("encrypted_content").String()
			thinking := responsesReasoningText(objectValueFromResult(item))
			if strings.HasPrefix(encrypted, redactedThinkingPrefix) {
				block, err := json.Marshal(map[string]any{"type": "redacted_thinking", "data": strings.TrimPrefix(encrypted, redactedThinkingPrefix)})
				if err != nil {
					return nil, fmt.Errorf("restore Claude redacted reasoning block")
				}
				blocks = append(blocks, block)
			} else if encrypted != "" || thinking != "" {
				block, err := json.Marshal(map[string]any{"type": "thinking", "thinking": thinking, "signature": encrypted})
				if err != nil {
					return nil, fmt.Errorf("restore Claude thinking block")
				}
				blocks = append(blocks, block)
			} else {
				return nil, fmt.Errorf("Chat Responses reasoning item has no replayable content")
			}
		default:
			return nil, fmt.Errorf("Chat reasoning_opaque contains an unsupported reasoning block")
		}
	}
	return blocks, nil
}

func objectValueFromResult(result gjson.Result) map[string]any {
	var value map[string]any
	if err := json.Unmarshal([]byte(result.Raw), &value); err != nil {
		return nil
	}
	return value
}

func validateChatAssistantOpaqueAnchor(messages [][]byte, messageIndex int, anchor copilotOpaqueAnchor) error {
	if anchor.ContentSHA256 != "" || len(anchor.ToolCallIDs) > 0 {
		anchoredIndex, _, err := findOpaqueAnchor(messages, anchor)
		if err != nil {
			return err
		}
		if anchoredIndex != messageIndex {
			return fmt.Errorf("Copilot opaque reasoning assistant anchor does not match its message")
		}
		return nil
	}
	if anchor.PriorUserSHA256 != "" && previousUserFingerprint(messages, messageIndex) == anchor.PriorUserSHA256 {
		matches := 0
		matchedIndex := -1
		for index, message := range messages {
			if gjson.GetBytes(message, "role").String() == "assistant" && previousUserFingerprint(messages, index) == anchor.PriorUserSHA256 && gjson.GetBytes(message, "content").String() == "" && len(gjson.GetBytes(message, "tool_calls").Array()) == 0 {
				matches++
				matchedIndex = index
			}
		}
		if matches == 1 && matchedIndex == messageIndex {
			return nil
		}
	}
	return fmt.Errorf("Copilot opaque reasoning assistant anchor is missing or ambiguous")
}

func restoreChatOpaqueToResponses(responses, chatRequest []byte, model string) ([]byte, error) {
	messages := gjson.GetBytes(chatRequest, "messages")
	input := gjson.GetBytes(responses, "input")
	if !messages.IsArray() || !input.IsArray() {
		return responses, nil
	}
	insertions := make(map[int][][]byte)
	cursor := 0
	for messageIndex, message := range messages.Array() {
		if message.Get("role").String() == "assistant" {
			field := message.Get("reasoning_opaque")
			insertIndex, matched, err := responsesInputIndexForChatAssistant(input.Array(), message, cursor)
			if err != nil {
				return nil, err
			}
			if matched {
				cursor = insertIndex + 1
			}
			if field.Exists() && field.Type != gjson.Null && field.Raw != `""` {
				var raw []byte
				if field.Type == gjson.String && bytes.HasPrefix([]byte(field.String()), []byte(copilotOpaquePrefix)) {
					value, err := decodeCopilotOpaque(field.String(), model)
					if err != nil {
						return nil, err
					}
					anchoredIndex, _, err := findOpaqueAnchor(chatMessageBytes(messages.Array()), value.Anchor)
					if err != nil {
						return nil, err
					}
					if anchoredIndex != messageIndex {
						return nil, fmt.Errorf("Copilot opaque reasoning assistant anchor does not match its message")
					}
					raw = value.Raw
				} else {
					raw = []byte(field.Raw)
				}
				items, err := responsesReasoningItemsFromChatOpaque(raw)
				if err != nil {
					return nil, err
				}
				if !matched {
					return nil, fmt.Errorf("Chat reasoning_opaque assistant has no Responses history position")
				}
				insertions[insertIndex] = append(insertions[insertIndex], items...)
			}
		}
	}
	if len(insertions) == 0 {
		return responses, nil
	}
	items := make([][]byte, 0, len(input.Array())+len(insertions))
	for index := 0; index <= len(input.Array()); index++ {
		items = append(items, insertions[index]...)
		if index < len(input.Array()) {
			items = append(items, []byte(input.Array()[index].Raw))
		}
	}
	return setRawArray(responses, "input", items)
}

func chatMessageBytes(messages []gjson.Result) [][]byte {
	values := make([][]byte, 0, len(messages))
	for _, message := range messages {
		values = append(values, []byte(message.Raw))
	}
	return values
}

func responsesReasoningItemsFromChatOpaque(raw []byte) ([][]byte, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("Chat reasoning_opaque is not valid JSON")
	}
	value := gjson.ParseBytes(raw)
	if value.Type == gjson.String {
		return nil, fmt.Errorf("Chat-native reasoning_opaque cannot be replayed to the Responses endpoint")
	}
	if value.IsObject() && value.Get("type").String() == "reasoning" {
		return [][]byte{raw}, nil
	}
	if !value.IsArray() {
		return nil, fmt.Errorf("Chat reasoning_opaque cannot be represented as Responses reasoning")
	}
	items := make([][]byte, 0, len(value.Array()))
	for _, item := range value.Array() {
		if item.IsObject() && item.Get("type").String() == "reasoning" && item.Get("encrypted_content").Type == gjson.String {
			items = append(items, []byte(item.Raw))
		} else if item.IsObject() && item.Get("type").String() == "thinking" {
			thinking := item.Get("thinking").String()
			summary := []any{}
			if thinking != "" {
				summary = append(summary, map[string]any{"type": "summary_text", "text": thinking})
			}
			reasoning := map[string]any{"type": "reasoning", "summary": summary, "encrypted_content": item.Get("signature").String()}
			encoded, err := json.Marshal(reasoning)
			if err != nil {
				return nil, fmt.Errorf("translate Chat thinking block for Responses replay")
			}
			items = append(items, encoded)
		} else if item.IsObject() && item.Get("type").String() == "redacted_thinking" && item.Get("data").Type == gjson.String {
			reasoning := map[string]any{"type": "reasoning", "summary": []any{}, "encrypted_content": redactedThinkingPrefix + item.Get("data").String()}
			encoded, err := json.Marshal(reasoning)
			if err != nil {
				return nil, fmt.Errorf("translate Chat redacted reasoning block for Responses replay")
			}
			items = append(items, encoded)
		} else {
			return nil, fmt.Errorf("Chat reasoning_opaque contains an unsupported Responses item")
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("Chat reasoning_opaque contains no Responses items")
	}
	return items, nil
}

func responsesInputIndexForChatAssistant(input []gjson.Result, message gjson.Result, start int) (int, bool, error) {
	for _, call := range message.Get("tool_calls").Array() {
		visibleID := call.Get("id").String()
		itemID, callID, encoded, err := parseClaudeToolID(visibleID)
		if err != nil {
			return 0, false, fmt.Errorf("Chat tool call ID has an invalid Responses identity")
		}
		if !encoded {
			callID = visibleID
		}
		for index, item := range input[start:] {
			index += start
			if item.Get("type").String() == "function_call" && item.Get("call_id").String() == callID {
				return index, true, nil
			}
		}
		if itemID != "" {
			for index, item := range input[start:] {
				index += start
				if item.Get("type").String() == "function_call" && item.Get("id").String() == itemID {
					return index, true, nil
				}
			}
		}
	}
	content, ok := chatContentFingerprint(message.Get("content"))
	if ok && content != "" {
		for index, item := range input[start:] {
			index += start
			if item.Get("type").String() == "message" && item.Get("role").String() == "assistant" {
				for _, part := range item.Get("content").Array() {
					if part.Get("text").String() == content {
						return index, true, nil
					}
				}
			}
		}
	}
	return len(input), false, nil
}

func chatRequestToClaudeToolIDs(body []byte) ([]byte, error) {
	if !json.Valid(body) {
		return nil, fmt.Errorf("request body is not valid JSON")
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body, nil
	}
	out := body
	calls := make(map[string]string)
	callIDs := make(map[string]struct{})
	for messageIndex, message := range messages.Array() {
		if message.Get("role").String() == "assistant" {
			for callIndex, call := range message.Get("tool_calls").Array() {
				visibleID := call.Get("id").String()
				itemID, callID, encoded, err := parseClaudeToolID(visibleID)
				if err != nil {
					return nil, fmt.Errorf("Chat tool call ID has an invalid Responses identity")
				}
				if !encoded {
					callID = visibleID
				}
				if callID == "" {
					return nil, fmt.Errorf("Chat tool call has no ID for Responses translation")
				}
				if _, exists := callIDs[callID]; exists {
					return nil, fmt.Errorf("Chat tool call IDs are ambiguous for Responses translation")
				}
				callIDs[callID] = struct{}{}
				if _, exists := calls[visibleID]; exists {
					return nil, fmt.Errorf("Chat tool call IDs are ambiguous for Responses translation")
				}
				carrier := encodeClaudeToolID(itemID, callID)
				if carrier == "" {
					return nil, fmt.Errorf("Chat tool call ID cannot be represented in Responses")
				}
				calls[visibleID] = carrier
				updated, err := sjson.SetBytes(out, fmt.Sprintf("messages.%d.tool_calls.%d.id", messageIndex, callIndex), carrier)
				if err != nil {
					return nil, fmt.Errorf("encode Chat tool call ID for Responses translation")
				}
				out = updated
			}
		}
	}
	for messageIndex, message := range messages.Array() {
		if message.Get("role").String() == "tool" {
			visibleID := message.Get("tool_call_id").String()
			if visibleID == "" {
				return nil, fmt.Errorf("Chat tool result has no call ID for Responses translation")
			}
			carrier, exists := calls[visibleID]
			if !exists {
				itemID, callID, encoded, err := parseClaudeToolID(visibleID)
				if err != nil {
					return nil, fmt.Errorf("Chat tool result ID has an invalid Responses identity")
				}
				if !encoded {
					callID = visibleID
				}
				carrier = encodeClaudeToolID(itemID, callID)
			}
			updated, err := sjson.SetBytes(out, fmt.Sprintf("messages.%d.tool_call_id", messageIndex), carrier)
			if err != nil {
				return nil, fmt.Errorf("encode Chat tool result ID for Responses translation")
			}
			out = updated
		}
	}
	return out, nil
}

func validateChatRequestForResponses(body []byte) error {
	root, err := decodeObject(body)
	if err != nil {
		return fmt.Errorf("decode Chat request for Responses translation")
	}
	messages, exists := root["messages"]
	if exists && hasMeaningfulValue(messages) {
		array, ok := messages.([]any)
		if !ok {
			return fmt.Errorf("Chat messages have an unsupported shape for Responses translation")
		}
		for _, rawMessage := range array {
			if message, ok := rawMessage.(map[string]any); ok {
				if err := validateChatMessageForResponses(message); err != nil {
					return err
				}
			} else if hasMeaningfulValue(rawMessage) {
				return fmt.Errorf("Chat messages contain an unsupported non-object entry")
			}
		}
	}
	for _, rawTool := range arrayValue(root["tools"]) {
		tool, ok := rawTool.(map[string]any)
		if !ok || stringValue(tool["type"]) != "function" || stringValue(objectValue(tool["function"])["name"]) == "" {
			return fmt.Errorf("Chat tool cannot be represented by Responses translation")
		}
	}
	return nil
}

func validateChatMessageForResponses(message map[string]any) error {
	role := stringValue(message["role"])
	if role != "system" && role != "developer" && role != "user" && role != "assistant" && role != "tool" {
		return fmt.Errorf("unsupported Chat message role %q for Responses translation", role)
	}
	if err := validateChatContent(message["content"], role); err != nil {
		return err
	}
	if role == "tool" && rawStringValue(message["tool_call_id"]) == "" {
		return fmt.Errorf("Chat tool result has no call ID for Responses translation")
	}
	for _, rawCall := range arrayValue(message["tool_calls"]) {
		call, ok := rawCall.(map[string]any)
		if !ok || stringValue(call["type"]) != "function" {
			return fmt.Errorf("Chat tool call cannot be represented by Responses translation")
		}
		function := objectValue(call["function"])
		arguments := rawStringValue(function["arguments"])
		if arguments != "" {
			value, err := decodeObject([]byte(arguments))
			if err != nil || !json.Valid([]byte(arguments)) || value == nil {
				return fmt.Errorf("Chat tool arguments must be a JSON object for Responses translation")
			}
		}
	}
	return nil
}

func validateChatContent(content any, role string) error {
	if content == nil || !hasMeaningfulValue(content) {
		return nil
	}
	if _, ok := content.(string); ok {
		return nil
	}
	parts, ok := content.([]any)
	if !ok {
		return fmt.Errorf("Chat message content has an unsupported shape for Responses translation")
	}
	for _, rawPart := range parts {
		if part, ok := rawPart.(map[string]any); ok {
			switch stringValue(part["type"]) {
			case "text":
				if _, ok := part["text"].(string); !ok {
					return fmt.Errorf("Chat text block has no string value")
				}
			case "image_url":
				if role == "system" || role == "developer" || rawStringValue(objectValue(part["image_url"])["url"]) == "" {
					return fmt.Errorf("Chat image block cannot be represented in this Responses message")
				}
			case "file":
				fileData := rawStringValue(objectValue(part["file"])["file_data"])
				if role == "system" || role == "developer" || !strings.HasPrefix(fileData, "data:") || !strings.Contains(fileData, ";") || !strings.Contains(fileData, ",") {
					return fmt.Errorf("Chat file block cannot be represented in this Responses message")
				}
			default:
				return fmt.Errorf("Chat content block %q cannot be represented by Responses translation", stringValue(part["type"]))
			}
		} else if _, isString := rawPart.(string); !isString || role != "tool" {
			return fmt.Errorf("Chat message contains a content block that Responses cannot represent")
		}
	}
	return nil
}

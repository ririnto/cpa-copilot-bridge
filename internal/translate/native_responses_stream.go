package translate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const nativeWebSearchBufferLimit = 8 << 20

type nativeResponsesWebSearchState struct {
	Frames  [][]byte
	Bytes   int
	DoneIDs map[int]string
}

// Copilot's hosted search IDs carry opaque replay state and change between phases.
// Delay the ordered stream barrier until its completed snapshot provides the
// final native identity; leave that authoritative snapshot unchanged.
func nativeResponsesStream(frame []byte, state *any) ([][]byte, error) {
	event, data, done, err := parseSSEFrame(frame)
	if err != nil {
		return nil, err
	}
	if event == "" {
		event = gjson.GetBytes(data, "type").String()
	}
	var pending *nativeResponsesWebSearchState
	if state != nil {
		pending, _ = (*state).(*nativeResponsesWebSearchState)
	}
	isSearchItem := gjson.GetBytes(data, "item.type").String() == "web_search_call"
	if pending == nil && !isSearchItem && !strings.HasPrefix(event, "response.web_search_call.") {
		return [][]byte{append([]byte(nil), frame...)}, nil
	}
	if state == nil {
		return nil, fmt.Errorf("native Responses web search stream requires state")
	}
	if pending == nil {
		pending = &nativeResponsesWebSearchState{DoneIDs: make(map[int]string)}
		*state = pending
	}
	if done || event == "response.failed" || event == "response.incomplete" || event == "error" {
		return nil, fmt.Errorf("native Responses web search stream ended without successful completion")
	}
	if isSearchItem || strings.HasPrefix(event, "response.web_search_call.") {
		indexValue := gjson.GetBytes(data, "output_index")
		index, err := strconv.Atoi(indexValue.Raw)
		if indexValue.Type != gjson.Number || err != nil || index < 0 {
			return nil, fmt.Errorf("native Responses web search event has an invalid output index")
		}
		if _, exists := pending.DoneIDs[index]; !exists {
			pending.DoneIDs[index] = ""
		}
		if event == "response.output_item.done" {
			id := gjson.GetBytes(data, "item.id")
			if id.Type != gjson.String || id.String() == "" || gjson.GetBytes(data, "item.status").String() != "completed" {
				return nil, fmt.Errorf("native Responses web search done item is incomplete")
			}
			pending.DoneIDs[index] = id.String()
		}
	}
	if len(frame) > nativeWebSearchBufferLimit-pending.Bytes {
		return nil, fmt.Errorf("native Responses web search stream exceeds the pending event limit")
	}
	pending.Frames = append(pending.Frames, append([]byte(nil), frame...))
	pending.Bytes += len(frame)
	if event != "response.completed" {
		return nil, nil
	}
	if gjson.GetBytes(data, "response.status").String() != "completed" {
		return nil, fmt.Errorf("native Responses web search completion is not successful")
	}
	finalIDs := make(map[int]string, len(pending.DoneIDs))
	for index, doneID := range pending.DoneIDs {
		item := gjson.GetBytes(data, fmt.Sprintf("response.output.%d", index))
		id := item.Get("id")
		if doneID == "" || item.Get("type").String() != "web_search_call" || item.Get("status").String() != "completed" || id.Type != gjson.String || id.String() == "" {
			return nil, fmt.Errorf("native Responses completed without the completed web search item at output index %d", index)
		}
		finalIDs[index] = id.String()
	}
	out := make([][]byte, 0, len(pending.Frames))
	for _, buffered := range pending.Frames {
		name, payload, _, err := parseSSEFrame(buffered)
		if err != nil {
			return nil, err
		}
		if len(payload) == 0 {
			out = append(out, buffered)
			continue
		}
		kind := gjson.GetBytes(payload, "type").String()
		if kind == "response.completed" {
			out = append(out, buffered)
			continue
		}
		root, err := decodeObject(payload)
		if err != nil {
			return nil, err
		}
		index, err := strconv.Atoi(numberKey(root["output_index"]))
		if err == nil {
			if id, exists := finalIDs[index]; exists {
				path := ""
				if gjson.GetBytes(payload, "item.type").String() == "web_search_call" {
					path = "item.id"
				} else if strings.HasPrefix(kind, "response.web_search_call.") {
					path = "item_id"
				}
				if path != "" {
					payload, err = sjson.SetBytes(payload, path, id)
					if err != nil {
						return nil, fmt.Errorf("retain terminal native web search identity: %w", err)
					}
					buffered = responseSSEBytes(name, payload)
				}
			}
		}
		out = append(out, buffered)
	}
	*state = nil
	return out, nil
}

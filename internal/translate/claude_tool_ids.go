package translate

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const claudeToolIDPrefix = "cpa_tool_v1_"

type claudeToolIDCarrier struct {
	ItemID string `json:"i,omitempty"`
	CallID string `json:"c"`
}

func encodeClaudeToolID(itemID, callID string) string {
	if itemID == "" && callID == "" {
		return ""
	}
	if ((itemID == "" && callID != "") || (itemID == callID && itemID != "")) && safeClaudeToolID(callID) && !strings.HasPrefix(callID, claudeToolIDPrefix) {
		return callID
	}
	encoded, err := json.Marshal(claudeToolIDCarrier{ItemID: itemID, CallID: callID})
	if err != nil {
		return callID
	}
	return claudeToolIDPrefix + base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeClaudeToolID(value string) (itemID, callID string, ok bool) {
	if !strings.HasPrefix(value, claudeToolIDPrefix) {
		return "", value, false
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, claudeToolIDPrefix))
	if err != nil {
		return "", value, false
	}
	var carrier claudeToolIDCarrier
	if errDecode := json.Unmarshal(encoded, &carrier); errDecode != nil || (carrier.CallID == "" && carrier.ItemID == "") {
		return "", value, false
	}
	return carrier.ItemID, carrier.CallID, true
}

// DecodeClaudeToolIDs returns the Responses IDs encoded in a Claude tool-use ID.
// The ok result is false when value is an ordinary, unencoded Claude tool ID.
func DecodeClaudeToolIDs(value string) (itemID, callID string, ok bool) {
	return decodeClaudeToolID(value)
}

func safeClaudeToolID(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func claudeToolIDFromResponses(item map[string]any) string {
	return encodeClaudeToolID(rawStringValue(item["id"]), rawStringValue(item["call_id"]))
}

func responsesToolIDsFromClaude(value string) (itemID, callID string) {
	if decodedItemID, decodedCallID, ok := decodeClaudeToolID(value); ok {
		return decodedItemID, decodedCallID
	}
	return "", value
}

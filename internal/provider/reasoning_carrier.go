package provider

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	translatorReasoningCarrierPrefix = "cpa-copilot-reasoning:v1:"
	sealedReasoningCarrierPrefix     = "cpa-copilot-reasoning-auth:v1:"
	reasoningCarrierNamespace        = "cpa-copilot-reasoning-auth:"
	reasoningCarrierMACDomain        = "cpa-copilot-bridge-reasoning-carrier-v1"
)

type reasoningCarrierScope struct {
	AuthID     string
	Credential string
	Model      string
	Endpoint   string
	APIBaseURL string
}

func reasoningCarrierScopeFor(authID string, storage authStorage, model, endpoint string, token copilotTokenEntry) reasoningCarrierScope {
	return reasoningCarrierScope{AuthID: authID, Credential: storage.GitHubAccessToken, Model: model, Endpoint: endpoint, APIBaseURL: token.APIBaseURL}
}

func unwrapRequestReasoningCarriers(sourceFormat string, payload []byte, scope reasoningCarrierScope) ([]byte, error) {
	return transformReasoningCarrierPaths(payload, requestReasoningCarrierPaths(sourceFormat, payload), func(value string) (string, bool, error) {
		if strings.HasPrefix(value, translatorReasoningCarrierPrefix) {
			return "", false, fmt.Errorf("unsealed Copilot reasoning carrier is not accepted")
		}
		if strings.HasPrefix(value, reasoningCarrierNamespace) {
			if !strings.HasPrefix(value, sealedReasoningCarrierPrefix) {
				return "", false, fmt.Errorf("unsupported Copilot reasoning carrier version")
			}
			inner, err := unsealReasoningCarrier(value, scope)
			return inner, err == nil, err
		}
		return value, false, nil
	})
}

func sealResponseReasoningCarriers(sourceFormat string, payload []byte, scope reasoningCarrierScope) ([]byte, error) {
	return transformReasoningCarrierPaths(payload, responseReasoningCarrierPaths(sourceFormat, payload), func(value string) (string, bool, error) {
		if strings.HasPrefix(value, sealedReasoningCarrierPrefix) {
			inner, err := unsealReasoningCarrier(value, scope)
			if err != nil {
				return "", false, err
			}
			sealed, err := sealReasoningCarrier(inner, scope)
			return sealed, err == nil, err
		}
		if strings.HasPrefix(value, translatorReasoningCarrierPrefix) {
			sealed, err := sealReasoningCarrier(value, scope)
			return sealed, err == nil, err
		}
		if strings.HasPrefix(value, reasoningCarrierNamespace) {
			return "", false, fmt.Errorf("unsupported Copilot reasoning carrier version")
		}
		return value, false, nil
	})
}

func sealReasoningCarrier(inner string, scope reasoningCarrierScope) (string, error) {
	if !strings.HasPrefix(inner, translatorReasoningCarrierPrefix) {
		return "", fmt.Errorf("invalid Copilot reasoning carrier")
	}
	secret, scopeID, err := reasoningCarrierKeyMaterial(scope)
	if err != nil {
		return "", err
	}
	mac := reasoningCarrierMAC(secret, scopeID, []byte(inner))
	return sealedReasoningCarrierPrefix + base64.RawURLEncoding.EncodeToString([]byte(inner)) + "." + base64.RawURLEncoding.EncodeToString(mac), nil
}

func unsealReasoningCarrier(sealed string, scope reasoningCarrierScope) (string, error) {
	if !strings.HasPrefix(sealed, sealedReasoningCarrierPrefix) {
		return "", fmt.Errorf("invalid Copilot reasoning carrier")
	}
	encoded := strings.TrimPrefix(sealed, sealedReasoningCarrierPrefix)
	payloadPart, macPart, ok := strings.Cut(encoded, ".")
	if !ok || payloadPart == "" || macPart == "" || strings.Contains(macPart, ".") {
		return "", fmt.Errorf("invalid Copilot reasoning carrier")
	}
	innerBytes, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil || !strings.HasPrefix(string(innerBytes), translatorReasoningCarrierPrefix) {
		return "", fmt.Errorf("invalid Copilot reasoning carrier")
	}
	providedMAC, err := base64.RawURLEncoding.DecodeString(macPart)
	if err != nil || len(providedMAC) != sha256.Size {
		return "", fmt.Errorf("invalid Copilot reasoning carrier")
	}
	secret, scopeID, err := reasoningCarrierKeyMaterial(scope)
	if err != nil {
		return "", err
	}
	if !hmac.Equal(providedMAC, reasoningCarrierMAC(secret, scopeID, innerBytes)) {
		return "", fmt.Errorf("Copilot reasoning carrier scope or authentication is invalid")
	}
	return string(innerBytes), nil
}

func reasoningCarrierKeyMaterial(scope reasoningCarrierScope) ([]byte, string, error) {
	if strings.TrimSpace(scope.Credential) == "" {
		return nil, "", fmt.Errorf("Copilot reasoning carrier scope is unavailable")
	}
	scopeID, secret := compactionKeyMaterial(scope.AuthID, scope.Credential, scope.Model, scope.Endpoint, scope.APIBaseURL)
	if scopeID == "" || len(secret) == 0 {
		return nil, "", fmt.Errorf("Copilot reasoning carrier scope is unavailable")
	}
	return secret, scopeID, nil
}

func reasoningCarrierMAC(secret []byte, scopeID string, inner []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(reasoningCarrierMACDomain))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(scopeID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(inner)
	return mac.Sum(nil)
}

func requestReasoningCarrierPaths(sourceFormat string, payload []byte) []string {
	root := gjson.ParseBytes(payload)
	paths := make([]string, 0)
	switch normalizeRequestFormat(sourceFormat) {
	case "claude":
		for messageIndex, message := range root.Get("messages").Array() {
			for blockIndex, block := range message.Get("content").Array() {
				if block.Get("type").String() == "thinking" && block.Get("signature").Type == gjson.String {
					paths = append(paths, fmt.Sprintf("messages.%d.content.%d.signature", messageIndex, blockIndex))
				}
			}
		}
	case "openai-response":
		for index, item := range root.Get("input").Array() {
			if item.Get("type").String() == "reasoning" && item.Get("encrypted_content").Type == gjson.String {
				paths = append(paths, fmt.Sprintf("input.%d.encrypted_content", index))
			}
		}
	}
	return paths
}

func responseReasoningCarrierPaths(sourceFormat string, payload []byte) []string {
	root := gjson.ParseBytes(payload)
	paths := make([]string, 0)
	switch normalizeRequestFormat(sourceFormat) {
	case "claude":
		appendClaudeThinkingSignaturePaths(&paths, root.Get("content"), "content")
	case "openai-response":
		appendResponsesEncryptedContentPaths(&paths, root.Get("output"), "output")
	}
	return paths
}

func streamReasoningCarrierPaths(destination string, payload []byte) []string {
	root := gjson.ParseBytes(payload)
	paths := make([]string, 0)
	switch normalizeRequestFormat(destination) {
	case "claude":
		if root.Get("content_block.type").String() == "thinking" {
			paths = appendStringPath(&paths, root.Get("content_block.signature"), "content_block.signature")
		}
		if root.Get("delta.type").String() == "signature_delta" {
			paths = appendStringPath(&paths, root.Get("delta.signature"), "delta.signature")
		}
		appendClaudeThinkingSignaturePaths(&paths, root.Get("content"), "content")
	case "openai-response":
		if root.Get("item.type").String() == "reasoning" {
			paths = appendStringPath(&paths, root.Get("item.encrypted_content"), "item.encrypted_content")
		}
		appendResponsesEncryptedContentPaths(&paths, root.Get("output"), "output")
		appendResponsesEncryptedContentPaths(&paths, root.Get("response.output"), "response.output")
	}
	return paths
}

func appendClaudeThinkingSignaturePaths(paths *[]string, blocks gjson.Result, base string) {
	for index, block := range blocks.Array() {
		if block.Get("type").String() == "thinking" && block.Get("signature").Type == gjson.String {
			*paths = append(*paths, fmt.Sprintf("%s.%d.signature", base, index))
		}
	}
}

func appendResponsesEncryptedContentPaths(paths *[]string, items gjson.Result, base string) {
	for index, item := range items.Array() {
		if item.Get("type").String() == "reasoning" && item.Get("encrypted_content").Type == gjson.String {
			*paths = append(*paths, fmt.Sprintf("%s.%d.encrypted_content", base, index))
		}
	}
}

func appendStringPath(paths *[]string, value gjson.Result, path string) []string {
	if value.Type == gjson.String {
		*paths = append(*paths, path)
	}
	return *paths
}

func transformReasoningCarrierPaths(payload []byte, paths []string, transform func(string) (string, bool, error)) ([]byte, error) {
	if len(paths) == 0 {
		return payload, nil
	}
	updated := payload
	for _, path := range paths {
		value := gjson.GetBytes(payload, path)
		if value.Type != gjson.String {
			continue
		}
		transformed, changed, err := transform(value.String())
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		var errSet error
		updated, errSet = sjson.SetBytes(updated, path, transformed)
		if errSet != nil {
			return nil, fmt.Errorf("update Copilot reasoning carrier: %w", errSet)
		}
	}
	return updated, nil
}

func sealReasoningCarrierSSEFrame(destination string, frame []byte, scope reasoningCarrierScope) ([]byte, error) {
	var output bytes.Buffer
	changed := false
	for offset := 0; offset < len(frame); {
		lineEnd := bytes.IndexByte(frame[offset:], '\n')
		lineLength := len(frame) - offset
		if lineEnd >= 0 {
			lineLength = lineEnd + 1
		}
		line := frame[offset : offset+lineLength]
		contentEnd := len(line)
		if contentEnd > 0 && line[contentEnd-1] == '\n' {
			contentEnd--
		}
		if contentEnd > 0 && line[contentEnd-1] == '\r' {
			contentEnd--
		}
		content := line[:contentEnd]
		if bytes.HasPrefix(content, []byte("data:")) {
			dataStart := len("data:")
			if dataStart < len(content) && content[dataStart] == ' ' {
				dataStart++
			}
			payload := content[dataStart:]
			if gjson.ValidBytes(payload) {
				sealed, err := sealStreamReasoningCarriers(destination, payload, scope)
				if err != nil {
					return nil, err
				}
				if !bytes.Equal(sealed, payload) {
					output.Write(line[:dataStart])
					output.Write(sealed)
					output.Write(line[contentEnd:])
					changed = true
					offset += lineLength
					continue
				}
			}
		}
		output.Write(line)
		offset += lineLength
	}
	if !changed {
		return frame, nil
	}
	return output.Bytes(), nil
}

func sealStreamReasoningCarriers(destination string, payload []byte, scope reasoningCarrierScope) ([]byte, error) {
	return transformReasoningCarrierPaths(payload, streamReasoningCarrierPaths(destination, payload), func(value string) (string, bool, error) {
		if strings.HasPrefix(value, sealedReasoningCarrierPrefix) {
			inner, err := unsealReasoningCarrier(value, scope)
			if err != nil {
				return "", false, err
			}
			sealed, err := sealReasoningCarrier(inner, scope)
			return sealed, err == nil, err
		}
		if strings.HasPrefix(value, translatorReasoningCarrierPrefix) {
			sealed, err := sealReasoningCarrier(value, scope)
			return sealed, err == nil, err
		}
		if strings.HasPrefix(value, reasoningCarrierNamespace) {
			return "", false, fmt.Errorf("unsupported Copilot reasoning carrier version")
		}
		return value, false, nil
	})
}

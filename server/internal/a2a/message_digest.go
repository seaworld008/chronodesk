package a2a

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

type canonicalSendMessageRequest struct {
	Tenant   string                  `json:"tenant,omitempty"`
	Message  Message                 `json:"message"`
	Push     *PushNotificationConfig `json:"taskPushNotificationConfig,omitempty"`
	Metadata map[string]any          `json:"metadata,omitempty"`
}

func sendMessageRequestDigest(params SendMessageParams) (string, error) {
	message := params.Message
	message.MessageID = strings.TrimSpace(message.MessageID)
	// Task/context IDs are server-resolved linkage. Replay validation checks
	// supplied values against the persisted Task, so adding the returned IDs to
	// an otherwise identical retry does not change the command digest.
	message.TaskID = ""
	message.ContextID = ""
	message.Extensions = normalizedStringSet(message.Extensions)
	message.ReferenceTaskID = normalizedStringSet(message.ReferenceTaskID)

	var push *PushNotificationConfig
	if params.Configuration.TaskPushNotification != nil {
		copyConfig := *params.Configuration.TaskPushNotification
		copyConfig.TaskID = ""
		if copyConfig.Authentication != nil {
			auth := *copyConfig.Authentication
			copyConfig.Authentication = &auth
		}
		push = &copyConfig
	}
	payload, err := json.Marshal(canonicalSendMessageRequest{
		Tenant:   strings.TrimSpace(params.Tenant),
		Message:  message,
		Push:     push,
		Metadata: params.Metadata,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func normalizedStringSet(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

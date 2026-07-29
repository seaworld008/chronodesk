package a2a

import "testing"

func TestSendMessageRequestDigestCoversExecutionSemanticsOnly(t *testing.T) {
	text := "structured request"
	ticketID := uint(42)
	base := SendMessageParams{
		Tenant: "private",
		Message: Message{
			MessageID: "digest-coverage",
			ContextID: "context-1",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
			Metadata:  map[string]any{"skill": "ticket-work"},
		},
		Configuration: SendMessageConfiguration{
			AcceptedOutputModes: []string{"application/json"},
			TaskPushNotification: &PushNotificationConfig{
				URL:   "https://hooks.example.com/a2a",
				Token: "push-token",
				Authentication: &AuthenticationInfo{
					Scheme: "Bearer", Credentials: "push-secret",
				},
			},
		},
		Metadata: map[string]any{
			"input":                map[string]any{"operation": "resolve"},
			MetadataLinkedTicketID: ticketID,
		},
	}
	digest, err := sendMessageRequestDigest(base)
	if err != nil {
		t.Fatal(err)
	}

	history := 3
	responseOnly := base
	responseOnly.Configuration.HistoryLength = &history
	responseOnly.Configuration.ReturnImmediately = true
	responseOnly.Configuration.AcceptedOutputModes = []string{"text/plain"}
	responseDigest, err := sendMessageRequestDigest(responseOnly)
	if err != nil {
		t.Fatal(err)
	}
	if responseDigest != digest {
		t.Fatal("pure response options changed request digest")
	}

	otherTicket := uint(43)
	tests := []struct {
		name   string
		mutate func(*SendMessageParams)
	}{
		{
			name: "skill metadata",
			mutate: func(params *SendMessageParams) {
				params.Message.Metadata = map[string]any{"skill": "ticket-comment"}
			},
		},
		{
			name: "linked ticket",
			mutate: func(params *SendMessageParams) {
				params.Metadata = map[string]any{
					"input":                map[string]any{"operation": "resolve"},
					MetadataLinkedTicketID: otherTicket,
				}
			},
		},
		{
			name: "push configuration",
			mutate: func(params *SendMessageParams) {
				copyConfig := *params.Configuration.TaskPushNotification
				copyConfig.Token = "different-token"
				params.Configuration.TaskPushNotification = &copyConfig
			},
		},
		{
			name: "command metadata",
			mutate: func(params *SendMessageParams) {
				params.Metadata = map[string]any{"input": map[string]any{"operation": "close"}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := base
			tt.mutate(&candidate)
			got, err := sendMessageRequestDigest(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if got == digest {
				t.Fatalf("%s did not change request digest", tt.name)
			}
		})
	}
}

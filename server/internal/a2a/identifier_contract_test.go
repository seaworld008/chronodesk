package a2a

import (
	"strings"
	"testing"
)

func TestInboundIdentifierLimitsUseUnicodeCharacters(t *testing.T) {
	text := "work"
	valid := Message{
		MessageID: strings.Repeat("智", maxA2AMessageIDLength),
		ContextID: strings.Repeat("界", maxA2AContextIDLength),
		TaskID:    strings.Repeat("任", maxA2ATaskIDLength),
		Role:      RoleUser,
		Parts:     []Part{{Text: &text}},
	}
	if err := valid.ValidateInbound(); err != nil {
		t.Fatalf("valid Unicode identifier boundaries were rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Message)
	}{
		{
			name: "message id",
			mutate: func(message *Message) {
				message.MessageID = strings.Repeat("智", maxA2AMessageIDLength+1)
			},
		},
		{
			name: "context id",
			mutate: func(message *Message) {
				message.ContextID = strings.Repeat("界", maxA2AContextIDLength+1)
			},
		},
		{
			name: "task id",
			mutate: func(message *Message) {
				message.TaskID = strings.Repeat("任", maxA2ATaskIDLength+1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := candidate.ValidateInbound(); err == nil {
				t.Fatal("oversized identifier was accepted")
			}
		})
	}
}

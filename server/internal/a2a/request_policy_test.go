package a2a

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestCanonicalRequestPolicyPayloadIgnoresObjectKeyOrder(t *testing.T) {
	first := JSONRPCRequest{
		Method: "SendMessage",
		Params: json.RawMessage(`{
			"metadata":{"z":2,"a":1},
			"message":{
				"parts":[{"metadata":{"z":2,"a":1},"text":"work"}],
				"role":"ROLE_USER",
				"messageId":"message-canonical"
			}
		}`),
	}
	second := JSONRPCRequest{
		Method: "SendMessage",
		Params: json.RawMessage(`{
			"message":{
				"messageId":"message-canonical",
				"role":"ROLE_USER",
				"parts":[{"text":"work","metadata":{"a":1,"z":2}}]
			},
			"metadata":{"a":1,"z":2}
		}`),
	}
	firstPayload, err := CanonicalRequestPolicyPayload(first)
	if err != nil {
		t.Fatal(err)
	}
	secondPayload, err := CanonicalRequestPolicyPayload(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstPayload, secondPayload) {
		t.Fatalf(
			"equivalent requests produced different payloads:\n%s\n%s",
			firstPayload,
			secondPayload,
		)
	}

	withUnknown := second
	withUnknown.Params = json.RawMessage(`{
		"message":{
			"messageId":"message-canonical",
			"role":"ROLE_USER",
			"parts":[{
				"text":"work",
				"metadata":{"a":1,"z":2},
				"futurePartField":true
			}],
			"futureMessageField":"ignored"
		},
		"metadata":{"a":1,"z":2},
		"futureRequestField":{"ignored":true}
	}`)
	unknownPayload, err := CanonicalRequestPolicyPayload(withUnknown)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(secondPayload, unknownPayload) {
		t.Fatalf(
			"unrecognized fields changed semantic policy payload:\n%s\n%s",
			secondPayload,
			unknownPayload,
		)
	}
}

func TestStrictRequestDecoderRejectsDuplicateKeys(t *testing.T) {
	_, err := DecodeJSONRPCRequest([]byte(`{
		"jsonrpc":"2.0",
		"id":"request-1",
		"method":"GetTask",
		"params":{"id":"task-allowed","id":"task-denied"}
	}`))
	if err == nil {
		t.Fatal("duplicate params key was accepted")
	}

	_, err = DecodeJSONRPCRequest([]byte(`{
		"jsonrpc":"2.0",
		"id":"request-1",
		"method":"GetTask",
		"method":"ListTasks",
		"params":{}
	}`))
	if err == nil {
		t.Fatal("duplicate method key was accepted")
	}
}

func TestCreatePushRequiresCanonicalAndRiskyPolicyActions(t *testing.T) {
	policies, err := ClassifyRequestPolicies(JSONRPCRequest{
		Method: "CreateTaskPushNotificationConfig",
		Params: json.RawMessage(`{
			"taskId":"task-1",
			"url":"https://events.example.test/a2a"
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 2 {
		t.Fatalf("create push policies = %#v", policies)
	}
	if policies[0].Action != "a2a.CreateTaskPushNotificationConfig" ||
		!policies[0].Write ||
		policies[0].Risky {
		t.Fatalf("canonical create-push policy = %+v", policies[0])
	}
	if policies[1].Action != "a2a.push.configure" ||
		!policies[1].Write ||
		!policies[1].Risky {
		t.Fatalf("risky create-push policy = %+v", policies[1])
	}
}

func TestRequestPolicyRejectsPre10MethodAliases(t *testing.T) {
	for _, method := range []string{
		"message/send",
		"message/stream",
		"tasks/get",
		"tasks/list",
		"tasks/cancel",
		"tasks/resubscribe",
		"tasks/pushNotificationConfig/set",
		"tasks/pushNotificationConfig/get",
		"tasks/pushNotificationConfig/list",
		"tasks/pushNotificationConfig/delete",
	} {
		t.Run(method, func(t *testing.T) {
			_, err := ClassifyRequestPolicies(JSONRPCRequest{
				Method: method,
				Params: json.RawMessage(`{}`),
			})
			var notFound methodNotFoundError
			if !errors.As(err, &notFound) {
				t.Fatalf("expected method-not-found, got %v", err)
			}
		})
	}
}

func TestGetExtendedAgentCardIsCanonicalReadPolicy(t *testing.T) {
	policies, err := ClassifyRequestPolicies(JSONRPCRequest{
		Method: "GetExtendedAgentCard",
		Params: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 1 ||
		policies[0].Action != "a2a.GetExtendedAgentCard" ||
		policies[0].Write ||
		policies[0].Risky {
		t.Fatalf("extended Agent Card policy = %#v", policies)
	}
}

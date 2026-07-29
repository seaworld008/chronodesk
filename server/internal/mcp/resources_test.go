package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCapabilitiesDescribeStatelessCoreAndSubscriptionRecovery(t *testing.T) {
	server := &Server{
		config: defaultConfig(),
		tools:  toolDefinitions(),
	}
	content, err := server.readResource(
		context.Background(),
		Principal{Scopes: []string{"*"}},
		resourceCapabilities,
	)
	if err != nil {
		t.Fatalf("read capabilities: %v", err)
	}

	var capabilities map[string]any
	if err := json.Unmarshal([]byte(content.Text), &capabilities); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}

	protocol := capabilityObject(t, capabilities, "protocol")
	if protocol["version"] != ProtocolVersion {
		t.Fatalf("protocol.version=%v, want %s", protocol["version"], ProtocolVersion)
	}
	transport := capabilityObject(t, protocol, "transport")
	if transport["name"] != "streamable-http" || transport["state_model"] != "stateless" {
		t.Fatalf("protocol.transport=%#v", transport)
	}
	session := capabilityObject(t, protocol, "session")
	if session["supported"] != false {
		t.Fatalf("protocol.session=%#v", session)
	}

	subscriptions := capabilityObject(t, capabilities, "subscriptions")
	if subscriptions["supported"] != true ||
		subscriptions["method"] != "subscriptions/listen" ||
		subscriptions["transport"] != "sse" ||
		subscriptions["delivery"] != "best-effort" {
		t.Fatalf("subscriptions=%#v", subscriptions)
	}
	streamResumption := capabilityObject(t, subscriptions, "stream_resumption")
	if streamResumption["supported"] != false {
		t.Fatalf("subscriptions.stream_resumption=%#v", streamResumption)
	}

	recovery := capabilityObject(t, capabilities, "durable_event_recovery")
	if recovery["supported"] != true ||
		recovery["transport"] != "rest" ||
		recovery["endpoint"] != "/api/v1/events" ||
		recovery["cursor"] != "opaque" ||
		recovery["required_scope"] != ScopeEventsSubscribe {
		t.Fatalf("durable_event_recovery=%#v", recovery)
	}
}

func TestCapabilitiesDoNotExposeRemovedFlatTransportFields(t *testing.T) {
	server := &Server{
		config: defaultConfig(),
		tools:  toolDefinitions(),
	}
	content, err := server.readResource(
		context.Background(),
		Principal{Scopes: []string{"*"}},
		resourceCapabilities,
	)
	if err != nil {
		t.Fatalf("read capabilities: %v", err)
	}

	var capabilities map[string]any
	if err := json.Unmarshal([]byte(content.Text), &capabilities); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	for _, removed := range []string{
		"protocol_version",
		"supported_protocol_versions",
		"transport",
		"stateless",
		"protocol_sessions",
		"subscription_method",
		"resumable_streams",
	} {
		if _, exists := capabilities[removed]; exists {
			t.Errorf("removed capability field %q is still exposed", removed)
		}
	}
}

func capabilityObject(t *testing.T, parent map[string]any, name string) map[string]any {
	t.Helper()
	value, ok := parent[name].(map[string]any)
	if !ok {
		t.Fatalf("%s=%#v, want object", name, parent[name])
	}
	return value
}

package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
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
		recovery["endpoint"] != "/api/v2/projects/{projectKey}/events" ||
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

func TestProjectResourceTemplatesAreExplicitlyScoped(t *testing.T) {
	got := make([]string, 0, len(resourceTemplates()))
	for _, template := range resourceTemplates() {
		got = append(got, template.URITemplate)
	}
	want := []string{
		"ticket://projects/{projectKey}/tickets/{id}",
		"ticket://projects/{projectKey}/queues/{queue}",
		"ticket://projects/{projectKey}/tickets/{id}/history",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resource templates = %#v, want %#v", got, want)
	}
}

func TestParseProjectResourceURI(t *testing.T) {
	maxTicketID := ^uint(0)
	maxTicketIDText := strconv.FormatUint(uint64(maxTicketID), 10)
	overflowTicketIDText := "18446744073709551616"
	if strconv.IntSize == 32 {
		overflowTicketIDText = "4294967296"
	}
	tests := []struct {
		name string
		uri  string
		want ProjectResourceReference
		ok   bool
	}{
		{
			name: "ticket",
			uri:  "ticket://projects/OPS/tickets/42",
			want: ProjectResourceReference{ProjectKey: "OPS", Kind: ProjectResourceTicket, TicketID: 42},
			ok:   true,
		},
		{
			name: "history",
			uri:  "ticket://projects/OPS/tickets/42/history",
			want: ProjectResourceReference{ProjectKey: "OPS", Kind: ProjectResourceHistory, TicketID: 42},
			ok:   true,
		},
		{
			name: "queue",
			uri:  "ticket://projects/OPS/queues/triage",
			want: ProjectResourceReference{ProjectKey: "OPS", Kind: ProjectResourceQueue, Queue: "triage"},
			ok:   true,
		},
		{
			name: "native-width maximum ticket",
			uri:  "ticket://projects/OPS/tickets/" + maxTicketIDText,
			want: ProjectResourceReference{
				ProjectKey: "OPS",
				Kind:       ProjectResourceTicket,
				TicketID:   maxTicketID,
			},
			ok: true,
		},
		{
			name: "native-width maximum history",
			uri:  "ticket://projects/OPS/tickets/" + maxTicketIDText + "/history",
			want: ProjectResourceReference{
				ProjectKey: "OPS",
				Kind:       ProjectResourceHistory,
				TicketID:   maxTicketID,
			},
			ok: true,
		},
		{
			name: "native-width overflow ticket",
			uri:  "ticket://projects/OPS/tickets/" + overflowTicketIDText,
		},
		{name: "legacy ticket URI", uri: "ticket://tickets/42"},
		{name: "legacy queue URI", uri: "ticket://queues/triage"},
		{name: "lowercase project", uri: "ticket://projects/ops/tickets/42"},
		{name: "zero ticket", uri: "ticket://projects/OPS/tickets/0"},
		{name: "leading-zero ticket", uri: "ticket://projects/OPS/tickets/042"},
		{name: "encoded separator", uri: "ticket://projects/OPS/tickets%2F42"},
		{name: "encoded project character", uri: "ticket://projects/%4fPS/tickets/42"},
		{name: "trailing slash", uri: "ticket://projects/OPS/tickets/42/"},
		{name: "query", uri: "ticket://projects/OPS/tickets/42?project=OTHER"},
		{name: "fragment", uri: "ticket://projects/OPS/tickets/42#history"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseProjectResourceURI(test.uri)
			if test.ok {
				if err != nil || !reflect.DeepEqual(got, test.want) {
					t.Fatalf("ParseProjectResourceURI() = (%#v, %v), want %#v", got, err, test.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("ParseProjectResourceURI(%q) unexpectedly succeeded: %#v", test.uri, got)
			}
		})
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

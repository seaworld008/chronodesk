package models

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestWorkflowVersionRequiresLockedLifecycleCategories(t *testing.T) {
	states := defaultConfigurationTestStates()
	states[1].LifecycleCategory = "processing"
	workflow := WorkflowVersion{
		OrganizationID: 1,
		ProjectID:      2,
		Key:            "default",
		Version:        1,
		Status:         ConfigurationStatusDraft,
		Name:           "Default",
	}
	if err := workflow.SetDefinitions(states, defaultConfigurationTestTransitions()); err == nil {
		t.Fatal("unsupported lifecycle category was accepted")
	}

	want := map[TicketStatus]LifecycleCategory{
		TicketStatusOpen:       LifecycleCategoryNew,
		TicketStatusInProgress: LifecycleCategoryActive,
		TicketStatusPending:    LifecycleCategoryWaiting,
		TicketStatusResolved:   LifecycleCategoryResolved,
		TicketStatusClosed:     LifecycleCategoryClosed,
		TicketStatusCancelled:  LifecycleCategoryCancelled,
	}
	for status, category := range want {
		got, err := DefaultLifecycleCategory(status)
		if err != nil || got != category {
			t.Errorf(
				"DefaultLifecycleCategory(%q) = %q, %v; want %q",
				status,
				got,
				err,
				category,
			)
		}
	}
}

func TestWorkflowVersionRejectsDuplicateLifecycleCategories(t *testing.T) {
	states := defaultConfigurationTestStates()
	states = append(states, WorkflowStateDefinition{
		Key:               "triage",
		Name:              "Triage",
		LifecycleCategory: LifecycleCategoryNew,
	})
	workflow := WorkflowVersion{
		OrganizationID: 1,
		ProjectID:      2,
		Key:            "duplicate-category",
		Version:        1,
		Status:         ConfigurationStatusDraft,
		Name:           "Duplicate category",
		CreatedByType:  ActorTypeHuman,
		CreatedByID:    HumanActor(1).ID,
	}
	encodedStates, err := json.Marshal(states)
	if err != nil {
		t.Fatal(err)
	}
	encodedTransitions, err := json.Marshal(defaultConfigurationTestTransitions())
	if err != nil {
		t.Fatal(err)
	}
	workflow.States = encodedStates
	workflow.Transitions = encodedTransitions

	err = workflow.Validate()
	const want = `duplicate workflow lifecycle category "new" in states "open" and "triage"`
	if err == nil || err.Error() != want {
		t.Fatalf("WorkflowVersion.Validate() error = %v, want %q", err, want)
	}
	if err := workflow.SetDefinitions(
		states,
		defaultConfigurationTestTransitions(),
	); err == nil || err.Error() != want {
		t.Fatalf("WorkflowVersion.SetDefinitions() error = %v, want %q", err, want)
	}
}

func TestWorkflowVersionAcceptsDefaultSixLifecycleCategories(t *testing.T) {
	states := []WorkflowStateDefinition{
		{
			Key: "open", Name: "Open",
			LifecycleCategory: LifecycleCategoryNew,
			IsInitial:         true,
		},
		{
			Key: "in_progress", Name: "In progress",
			LifecycleCategory: LifecycleCategoryActive,
		},
		{
			Key: "pending", Name: "Pending",
			LifecycleCategory: LifecycleCategoryWaiting,
		},
		{
			Key: "resolved", Name: "Resolved",
			LifecycleCategory: LifecycleCategoryResolved,
			IsTerminal:        true,
		},
		{
			Key: "closed", Name: "Closed",
			LifecycleCategory: LifecycleCategoryClosed,
			IsTerminal:        true,
		},
		{
			Key: "cancelled", Name: "Cancelled",
			LifecycleCategory: LifecycleCategoryCancelled,
			IsTerminal:        true,
		},
	}
	transitions := []WorkflowTransitionDefinition{
		{Key: "start", Name: "Start", From: "open", To: "in_progress"},
		{Key: "wait", Name: "Wait", From: "in_progress", To: "pending"},
		{Key: "resume", Name: "Resume", From: "pending", To: "in_progress"},
		{Key: "resolve", Name: "Resolve", From: "in_progress", To: "resolved"},
		{Key: "close", Name: "Close", From: "resolved", To: "closed"},
		{Key: "cancel", Name: "Cancel", From: "open", To: "cancelled"},
	}
	workflow := WorkflowVersion{
		OrganizationID: 1,
		ProjectID:      2,
		Key:            "default-six",
		Version:        1,
		Status:         ConfigurationStatusDraft,
		Name:           "Default six",
		CreatedByType:  ActorTypeHuman,
		CreatedByID:    HumanActor(1).ID,
	}
	if err := workflow.SetDefinitions(states, transitions); err != nil {
		t.Fatalf("default six-state workflow rejected: %v", err)
	}
}

func TestTypedExpressionsAndActionsRejectExecutableEscapeHatches(t *testing.T) {
	valid := TypedExpression{
		Field:     "ticket.priority",
		ValueType: ExpressionValueString,
		Operator:  ExpressionOperatorEqual,
		Value:     json.RawMessage(`"urgent"`),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid typed expression: %v", err)
	}
	invalid := valid
	invalid.Field = "ticket.priority;eval(script)"
	if err := invalid.Validate(); err == nil {
		t.Fatal("script-like expression field was accepted")
	}

	action := ConfigurationAction{
		Type: ConfigurationActionRouteQueue,
		Parameters: json.RawMessage(
			`{"queue_key":"default","script":"os.execute('unsafe')"}`,
		),
	}
	if err := action.Validate(); err == nil {
		t.Fatal("unknown executable action field was accepted")
	}
	action.Type = "run_script"
	action.Parameters = json.RawMessage(`{"source":"unsafe"}`)
	if err := action.Validate(); err == nil {
		t.Fatal("unregistered action type was accepted")
	}
}

func TestConfigurationSnapshotBoundsPublishedIntakeArrays(t *testing.T) {
	validID := "019fb344-fa16-7e13-9c5b-08eb95478098"
	oversized := make([]string, MaxConfigurationSnapshotVersions+1)
	for index := range oversized {
		oversized[index] = validID
	}
	for _, snapshot := range []ConfigurationSnapshot{
		{
			RequestTypeVersionIDs: oversized,
			WorkflowVersionIDs:    []string{validID},
		},
		{
			RequestTypeVersionIDs: []string{validID},
			WorkflowVersionIDs:    oversized,
		},
	} {
		if err := snapshot.Validate(); err == nil ||
			!strings.Contains(err.Error(), "maximum published version count") {
			t.Fatalf("oversized configuration snapshot error = %v", err)
		}
	}
}

func TestIndustrySolutionPackageEd25519DetectsTamperingAndExports(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := configurationTestSolutionSnapshot(false)
	manifest := configurationTestManifest(snapshot, "1.0.0")
	solution, err := SignIndustrySolutionPackage(
		manifest,
		snapshot,
		"test-key-1",
		privateKey,
	)
	if err != nil {
		t.Fatalf("sign package: %v", err)
	}
	if err := solution.Verify(publicKey); err != nil {
		t.Fatalf("verify signed package: %v", err)
	}
	exported, err := solution.Export()
	if err != nil {
		t.Fatalf("export package: %v", err)
	}
	parsed, err := ParseIndustrySolutionPackage(exported)
	if err != nil {
		t.Fatalf("parse exported package: %v", err)
	}
	if err := parsed.Verify(publicKey); err != nil {
		t.Fatalf("verify parsed package: %v", err)
	}

	tampered := *parsed
	tampered.Snapshot.RequestTypes = append(
		[]RequestTypeTemplate(nil),
		parsed.Snapshot.RequestTypes...,
	)
	tampered.Snapshot.RequestTypes[0].Name = "Tampered"
	if err := tampered.Verify(publicKey); !errors.Is(
		err,
		ErrIndustrySolutionSignatureInvalid,
	) {
		t.Fatalf("tampered package error = %v", err)
	}

	withUnknownField := bytes.Replace(
		exported,
		[]byte(`"signature_algorithm"`),
		[]byte(`"unexpected_control":"unsafe","signature_algorithm"`),
		1,
	)
	if _, err := ParseIndustrySolutionPackage(withUnknownField); err == nil {
		t.Fatal("unknown package field was accepted")
	}
}

func TestIndustrySolutionDiffReportsBreakingCompatibilityChanges(t *testing.T) {
	from := configurationTestSolutionSnapshot(false)
	to := configurationTestSolutionSnapshot(true)
	diff, err := DiffIndustrySolutionSnapshots("1.0.0", from, "1.1.0", to)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Compatible {
		t.Fatalf("breaking upgrade marked compatible: %+v", diff)
	}
	if !containsConfigurationString(diff.Changed, "request_type:incident") {
		t.Fatalf("request type change missing from diff: %+v", diff)
	}
	foundRequiredField := false
	for _, change := range diff.BreakingChanges {
		if strings.Contains(change, "added required field impact") {
			foundRequiredField = true
		}
	}
	if !foundRequiredField {
		t.Fatalf("required-field compatibility break missing: %+v", diff)
	}
}

func TestIndustrySolutionExtensionsParticipateInHashAndDiff(t *testing.T) {
	from := configurationTestSolutionSnapshot(false)
	from.Extensions = map[string]json.RawMessage{
		"reference_blueprint": json.RawMessage(
			`{"schema_version":"1.0","agent_templates":[{"key":"triage"}]}`,
		),
	}
	to := configurationTestSolutionSnapshot(false)
	to.Extensions = map[string]json.RawMessage{
		"reference_blueprint": json.RawMessage(
			`{"schema_version":"1.0","agent_templates":[{"key":"triage"},{"key":"resolver"}]}`,
		),
	}
	if err := from.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := to.Validate(); err != nil {
		t.Fatal(err)
	}
	fromHash, err := hashCanonicalJSON(from)
	if err != nil {
		t.Fatal(err)
	}
	toHash, err := hashCanonicalJSON(to)
	if err != nil {
		t.Fatal(err)
	}
	if fromHash == toHash {
		t.Fatal("solution extension change did not affect snapshot hash")
	}
	diff, err := DiffIndustrySolutionSnapshots("1.0.0", from, "1.1.0", to)
	if err != nil {
		t.Fatal(err)
	}
	if !containsConfigurationString(
		diff.Changed,
		"extension:reference_blueprint",
	) {
		t.Fatalf("solution extension change missing from diff: %+v", diff)
	}

	invalid := configurationTestSolutionSnapshot(false)
	invalid.Extensions = map[string]json.RawMessage{
		"reference_blueprint": json.RawMessage(`["not-an-object"]`),
	}
	if err := invalid.Validate(); err == nil {
		t.Fatal("non-object solution extension was accepted")
	}
}

func configurationTestSolutionSnapshot(
	addRequiredImpact bool,
) IndustrySolutionSnapshot {
	required := `["title"]`
	properties := `"title":{"type":"string"}`
	if addRequiredImpact {
		required = `["title","impact"]`
		properties += `,"impact":{"type":"string"}`
	}
	schema := json.RawMessage(
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{` +
			properties +
			`},"required":` +
			required +
			`,"additionalProperties":false}`,
	)
	return IndustrySolutionSnapshot{
		RequestTypes: []RequestTypeTemplate{
			{
				Key:        "incident",
				Name:       "Incident",
				WorkClass:  WorkClassIncident,
				JSONSchema: schema,
				UISchema:   json.RawMessage(`{}`),
			},
		},
		Workflows: []WorkflowTemplate{
			{
				Key:         "default",
				Name:        "Default",
				States:      defaultConfigurationTestStates(),
				Transitions: defaultConfigurationTestTransitions(),
			},
		},
	}
}

func configurationTestManifest(
	snapshot IndustrySolutionSnapshot,
	version string,
) IndustrySolutionManifest {
	references := []SolutionTemplateReference{
		{Kind: SolutionTemplateRequestType, Key: "incident"},
		{Kind: SolutionTemplateWorkflow, Key: "default"},
	}
	return IndustrySolutionManifest{
		SchemaVersion:      "1.0",
		PackageKey:         "it-operations",
		Name:               "IT Operations",
		Industry:           "technology",
		Version:            version,
		Terminology:        map[string]string{"ticket": "工单"},
		TemplateReferences: references,
	}
}

func defaultConfigurationTestStates() []WorkflowStateDefinition {
	return []WorkflowStateDefinition{
		{
			Key: "open", Name: "Open",
			LifecycleCategory: LifecycleCategoryNew,
			IsInitial:         true,
		},
		{
			Key: "in_progress", Name: "In progress",
			LifecycleCategory: LifecycleCategoryActive,
		},
		{
			Key: "resolved", Name: "Resolved",
			LifecycleCategory: LifecycleCategoryResolved,
			IsTerminal:        true,
		},
	}
}

func defaultConfigurationTestTransitions() []WorkflowTransitionDefinition {
	return []WorkflowTransitionDefinition{
		{
			Key: "start", Name: "Start",
			From: "open", To: "in_progress",
		},
		{
			Key: "resolve", Name: "Resolve",
			From: "in_progress", To: "resolved",
		},
	}
}

func containsConfigurationString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

package openapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/seaworld008/chronodesk/server/internal/agentcontract"
)

func TestSpecificationIsStableAgentContract(t *testing.T) {
	// Some YAML 1.1 parsers (including Ruby Psych, which is commonly used by
	// OpenAPI tooling) reject colon-containing plain scalars in flow
	// sequences even though yaml.v3 accepts them. Keep scopes and resource
	// references quoted so the published machine contract is portable.
	quotedScalar := regexp.MustCompile(`["'][^"'\n]*["']`)
	unquotedDocument := quotedScalar.ReplaceAll(Specification(), nil)
	unquotedFlowScalar := regexp.MustCompile(`(?:\[|,\s*)[A-Za-z0-9._-]+:[A-Za-z0-9._-]+(?:\s*[\],])`)
	if match := unquotedFlowScalar.Find(unquotedDocument); match != nil {
		t.Fatalf("quote colon-containing YAML flow scalar %q", match)
	}

	var document map[string]any
	if err := yaml.Unmarshal(Specification(), &document); err != nil {
		t.Fatalf("parse embedded OpenAPI document: %v", err)
	}
	if got := document["openapi"]; got != "3.2.0" {
		t.Fatalf("openapi = %v, want 3.2.0", got)
	}

	paths := contractMap(t, document["paths"], "paths")
	operationIDs := make(map[string]string)
	for path, rawPathItem := range paths {
		pathItem := contractMap(t, rawPathItem, "paths."+path)
		for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
			rawOperation, exists := pathItem[method]
			if !exists {
				continue
			}
			operation := contractMap(t, rawOperation, method+" "+path)
			operationID, ok := operation["operationId"].(string)
			if !ok || strings.TrimSpace(operationID) == "" {
				t.Fatalf("%s %s has no operationId", strings.ToUpper(method), path)
			}
			if previous, duplicate := operationIDs[operationID]; duplicate {
				t.Fatalf("duplicate operationId %q on %s and %s %s", operationID, previous, strings.ToUpper(method), path)
			}
			operationIDs[operationID] = strings.ToUpper(method) + " " + path
		}
	}

	for _, path := range []string{
		"/capabilities",
		"/tickets",
		"/tickets/{ticketId}",
		"/tickets/{ticketId}/commands/assign",
		"/tickets/{ticketId}/commands/transition",
		"/tickets/{ticketId}/commands/escalate",
		"/events",
		"/oauth/token",
		"/.well-known/oauth-protected-resource/mcp",
		"/.well-known/oauth-protected-resource/api/v1",
		"/.well-known/oauth-protected-resource/a2a/v1",
		"/.well-known/agent-card.json",
		"/mcp",
		"/a2a/v1",
		"/admin/leases/{leaseId}/force-release",
		"/admin/attachments/{attachmentId}/scan",
	} {
		if _, exists := paths[path]; !exists {
			t.Errorf("required protocol path %s is missing", path)
		}
	}

	assertHeaderRefs(t, paths, "/tickets", "post", "IdempotencyKey")
	assertHeaderRefs(t, paths, "/tickets/{ticketId}", "patch", "IfMatch", "IdempotencyKey", "TicketLease")
	for _, path := range []string{
		"/tickets/{ticketId}/commands/assign",
		"/tickets/{ticketId}/commands/transition",
		"/tickets/{ticketId}/commands/escalate",
	} {
		assertHeaderRefs(
			t,
			paths,
			path,
			"post",
			"IfMatch",
			"IdempotencyKey",
			"TicketLease",
			"CommandCorrelationId",
		)
	}
	assertHeaderRefs(t, paths, "/tickets/{ticketId}/comments", "post", "IfMatch", "IdempotencyKey", "TicketLease")
	assertHeaderRefs(t, paths, "/tickets/{ticketId}/attachments", "post", "IfMatch", "IdempotencyKey", "TicketLease")
	assertHeaderRefs(t, paths, "/tickets/{ticketId}/claim", "post", "IfMatch", "IdempotencyKey")
	assertHeaderRefs(t, paths, "/leases/{leaseId}/heartbeat", "post", "IfMatch", "IdempotencyKey")
	assertHeaderRefs(t, paths, "/leases/{leaseId}", "delete", "IdempotencyKey")

	patch := contractOperation(t, paths, "/tickets/{ticketId}", "patch")
	if !hasOAuthScopeAlternative(patch, "tickets:update") {
		t.Error("ordinary ticket patch has no tickets:update scope")
	}
	for _, forbiddenScope := range []string{"tickets:assign", "tickets:transition"} {
		if hasOAuthScopeAlternative(patch, forbiddenScope) {
			t.Errorf("ordinary ticket patch still advertises %s", forbiddenScope)
		}
	}
	for path, scope := range map[string]string{
		"/tickets/{ticketId}/commands/assign":     "tickets:assign",
		"/tickets/{ticketId}/commands/transition": "tickets:transition",
		"/tickets/{ticketId}/commands/escalate":   "tickets:transition",
	} {
		command := contractOperation(t, paths, path, "post")
		if !hasOAuthScopeAlternative(command, scope) {
			t.Errorf("%s has no %s scope", path, scope)
		}
	}

	components := contractMap(t, document["components"], "components")
	securitySchemes := contractMap(t, components["securitySchemes"], "components.securitySchemes")
	oauth2 := contractMap(t, securitySchemes["oauth2"], "components.securitySchemes.oauth2")
	flows := contractMap(t, oauth2["flows"], "components.securitySchemes.oauth2.flows")
	clientCredentials := contractMap(t, flows["clientCredentials"], "components.securitySchemes.oauth2.flows.clientCredentials")
	scopes := contractMap(t, clientCredentials["scopes"], "OAuth scopes")
	expectedScopes := agentcontract.SupportedScopes()
	for _, scope := range expectedScopes {
		if _, exists := scopes[scope]; !exists {
			t.Errorf("OAuth scope %q is missing", scope)
		}
	}
	if len(scopes) != len(expectedScopes) {
		t.Errorf("OAuth scopes = %d, want exactly %d least-privilege scopes", len(scopes), len(expectedScopes))
	}

	walkContract(document, "#", func(location, ref string) {
		if !strings.HasPrefix(ref, "#/") {
			return
		}
		if _, ok := resolveContractRef(document, ref); !ok {
			t.Errorf("unresolved local reference %q at %s", ref, location)
		}
	})

	webhooks := contractMap(t, document["webhooks"], "webhooks")
	domainEvent := contractMap(t, webhooks["domainEvent"], "webhooks.domainEvent")
	webhookPost := contractMap(t, domainEvent["post"], "webhooks.domainEvent.post")
	if webhookPost["operationId"] != "receiveChronoDeskDomainEvent" {
		t.Errorf("CloudEvent webhook operationId = %v", webhookPost["operationId"])
	}

	adminWriteSchemas := []struct {
		path       string
		method     string
		status     string
		schemaName string
	}{
		{path: "/admin/agent-control/read-only", method: "put", status: "200", schemaName: "BooleanControlEnvelope"},
		{path: "/admin/agent-control/emergency-stop", method: "put", status: "200", schemaName: "BooleanControlEnvelope"},
		{path: "/admin/service-principals", method: "post", status: "201", schemaName: "IssuedCredentialEnvelope"},
		{path: "/admin/service-principals/{principalId}/status", method: "put", status: "200", schemaName: "ServicePrincipalEnvelope"},
		{path: "/admin/service-principals/{principalId}/credentials/rotate", method: "post", status: "200", schemaName: "IssuedCredentialEnvelope"},
		{path: "/admin/service-principals/{principalId}/credentials/{credentialId}", method: "delete", status: "200", schemaName: "CredentialRevocationEnvelope"},
		{path: "/admin/service-principals/{principalId}/policies", method: "post", status: "201", schemaName: "AgentPolicyEnvelope"},
		{path: "/admin/service-principals/{principalId}/policies/{policyId}", method: "delete", status: "200", schemaName: "PolicyDisableEnvelope"},
		{path: "/admin/leases/{leaseId}/force-release", method: "post", status: "200", schemaName: "AdminTicketLeaseEnvelope"},
		{path: "/admin/attachments/{attachmentId}/scan", method: "post", status: "200", schemaName: "AttachmentScanEnvelope"},
		{path: "/admin/outbox/{deliveryId}/replay", method: "post", status: "202", schemaName: "ReplayEnvelope"},
	}
	schemas := contractMap(t, components["schemas"], "components.schemas")
	for _, schemaName := range []string{"Ticket", "TicketCreate", "TicketFieldPatch", "Comment", "CommentCreate"} {
		schema := contractMap(t, schemas[schemaName], "components.schemas."+schemaName)
		properties := contractMap(t, schema["properties"], "components.schemas."+schemaName+".properties")
		if _, exists := properties["attachments"]; exists {
			t.Errorf("%s exposes removed legacy attachment array", schemaName)
		}
		if _, exists := properties["attachment_ids"]; exists {
			t.Errorf("%s exposes unsupported attachment association input", schemaName)
		}
	}
	for _, removed := range []string{"TicketAssignmentPatch", "TicketTransitionPatch"} {
		if _, exists := schemas[removed]; exists {
			t.Errorf("legacy mixed-patch schema %s still exists", removed)
		}
	}
	for _, schemaName := range []string{
		"TicketFieldPatch",
		"TicketAssignCommand",
		"TicketTransitionCommand",
		"TicketEscalateCommand",
		"AssignableActorRef",
	} {
		schema := contractMap(t, schemas[schemaName], "components.schemas."+schemaName)
		if schema["additionalProperties"] != false {
			t.Errorf("ticket command schema %s is not closed", schemaName)
		}
	}
	for schemaName, requiredFields := range map[string][]string{
		"TicketAssignCommand":     {"assignee", "reason"},
		"TicketTransitionCommand": {"status", "reason"},
		"TicketEscalateCommand":   {"reason"},
	} {
		schema := contractMap(t, schemas[schemaName], "components.schemas."+schemaName)
		for _, field := range requiredFields {
			if !contractSliceContains(schema["required"], field) {
				t.Errorf("%s does not require %s", schemaName, field)
			}
		}
		properties := contractMap(
			t,
			schema["properties"],
			"components.schemas."+schemaName+".properties",
		)
		reason := contractMap(
			t,
			properties["reason"],
			"components.schemas."+schemaName+".properties.reason",
		)
		if reason["minLength"] != 1 || reason["maxLength"] != 1000 {
			t.Errorf(
				"%s reason bounds = [%v,%v], want [1,1000]",
				schemaName,
				reason["minLength"],
				reason["maxLength"],
			)
		}
	}
	ordinaryPatch := contractMap(t, schemas["TicketFieldPatch"], "components.schemas.TicketFieldPatch")
	ordinaryProperties := contractMap(t, ordinaryPatch["properties"], "TicketFieldPatch.properties")
	for _, forbidden := range []string{
		"status",
		"is_escalated",
		"source",
		"trust_level",
		"sla_breached",
		"sla_due_date",
		"assigned_to_id",
		"assigned_to_actor_type",
		"assigned_to_actor_id",
		"assigned_to_service_principal_id",
	} {
		if _, exists := ordinaryProperties[forbidden]; exists {
			t.Errorf("ordinary ticket patch exposes command/control field %s", forbidden)
		}
	}
	receiptSchemas := make(map[string]bool)
	declaredAdminWrites := make(map[string]bool, len(adminWriteSchemas))
	for _, expected := range adminWriteSchemas {
		declaredAdminWrites[strings.ToUpper(expected.method)+" "+expected.path] = true
		assertOperationResponseSchema(
			t,
			paths,
			expected.path,
			expected.method,
			expected.status,
			expected.schemaName,
		)
		receiptSchemas[expected.schemaName] = true
		assertResponseHeaderRef(
			t,
			paths,
			expected.path,
			expected.method,
			expected.status,
			"ETag",
			"ETag",
		)
		requiredHeaders := []string{"IdempotencyKey"}
		isTopLevelCreate := expected.path == "/admin/service-principals"
		if !isTopLevelCreate {
			requiredHeaders = append(requiredHeaders, "IfMatch")
			assertOperationResponseRef(
				t,
				paths,
				expected.path,
				expected.method,
				"428",
				"PreconditionRequired",
			)
			assertOperationResponseRef(
				t,
				paths,
				expected.path,
				expected.method,
				"409",
				"AdminConflict",
			)
		} else {
			assertOperationResponseRef(
				t,
				paths,
				expected.path,
				expected.method,
				"409",
				"Conflict",
			)
		}
		assertHeaderRefs(t, paths, expected.path, expected.method, requiredHeaders...)
		for status, responseName := range map[string]string{
			"400": "BadRequest",
			"401": "Unauthorized",
			"403": "Forbidden",
			"413": "PayloadTooLarge",
			"500": "InternalError",
			"503": "ServiceUnavailable",
		} {
			assertOperationResponseRef(
				t,
				paths,
				expected.path,
				expected.method,
				status,
				responseName,
			)
		}
		if !isTopLevelCreate &&
			expected.path != "/admin/agent-control/read-only" &&
			expected.path != "/admin/agent-control/emergency-stop" {
			assertOperationResponseRef(
				t,
				paths,
				expected.path,
				expected.method,
				"404",
				"NotFound",
			)
		}
		operation := contractOperation(t, paths, expected.path, expected.method)
		if !strings.Contains(strings.ToLower(fmt.Sprint(operation["description"])), "idempot") {
			t.Errorf("%s %s does not document exact idempotent replay", strings.ToUpper(expected.method), expected.path)
		}
	}
	assertEveryAdminWriteIsDeclared(t, paths, declaredAdminWrites)
	assertResponseHeaderRef(
		t,
		paths,
		"/admin/service-principals/{principalId}/policies",
		"post",
		"201",
		"X-Parent-ETag",
		"ParentETag",
	)
	for schemaName := range receiptSchemas {
		assertSchemaRequiresReceipt(t, schemas, schemaName)
	}
	assertNoStoreCredentialResponse(t, paths, "/admin/service-principals", "post", "201")
	assertNoStoreCredentialResponse(
		t,
		paths,
		"/admin/service-principals/{principalId}/credentials/rotate",
		"post",
		"200",
	)
	componentResponses := contractMap(t, components["responses"], "components.responses")
	adminConflict := contractMap(t, componentResponses["AdminConflict"], "components.responses.AdminConflict")
	adminConflictHeaders := contractMap(t, adminConflict["headers"], "AdminConflict.headers")
	adminConflictETag := contractMap(t, adminConflictHeaders["ETag"], "AdminConflict.headers.ETag")
	if got := adminConflictETag["$ref"]; got != "#/components/headers/ETag" {
		t.Errorf("AdminConflict ETag = %v", got)
	}

	assertAdminResourceVersionContract(t, schemas)
	assertMCP20260728Contract(t, paths, components, schemas)
	assertA2A10Contract(t, paths, components, schemas)
	assertMCPResourceBoundTokenContract(t, paths, schemas)
}

func TestSpecificationHasNoDuplicateKeysOrSemanticArrays(t *testing.T) {
	decoder := yaml.NewDecoder(bytes.NewReader(Specification()))
	var root yaml.Node
	if err := decoder.Decode(&root); err != nil {
		t.Fatalf("parse OpenAPI YAML node tree: %v", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("OpenAPI must contain exactly one YAML document, got %v", err)
	}
	assertUniqueYAMLMappingKeys(t, &root, "#")

	var document map[string]any
	if err := root.Decode(&document); err != nil {
		t.Fatalf("decode OpenAPI YAML node tree: %v", err)
	}
	assertUniqueContractArrays(t, document, "#")
}

func assertEveryAdminWriteIsDeclared(
	t *testing.T,
	paths map[string]any,
	declared map[string]bool,
) {
	t.Helper()
	found := make(map[string]bool)
	for path, rawPathItem := range paths {
		if !strings.HasPrefix(path, "/admin/") {
			continue
		}
		pathItem := contractMap(t, rawPathItem, "paths."+path)
		for _, method := range []string{"post", "put", "patch", "delete"} {
			if _, exists := pathItem[method]; !exists {
				continue
			}
			key := strings.ToUpper(method) + " " + path
			found[key] = true
			if !declared[key] {
				t.Errorf("administrator write %s is not covered by the strict command contract", key)
			}
		}
	}
	for key := range declared {
		if !found[key] {
			t.Errorf("declared administrator write %s does not exist", key)
		}
	}
}

func assertAdminResourceVersionContract(t *testing.T, schemas map[string]any) {
	t.Helper()

	closedSchemas := []string{
		"BooleanControl",
		"ServicePrincipalCreate",
		"ServicePrincipalControl",
		"ServicePrincipal",
		"IssuedCredential",
		"AgentPolicyCreate",
		"AgentPolicy",
		"AdminAgentPolicy",
		"AdminOverview",
		"AdminServicePrincipalSummary",
		"AdminTicketLeaseSummary",
		"AdminDomainEventSummary",
		"AdminOutboxDeliverySummary",
		"AdminAttachmentSummary",
		"AdminPolicyDecisionSummary",
		"CredentialRevocationResult",
		"PolicyDisableResult",
		"AdminReleasedTicketLease",
		"AttachmentScanUpdate",
	}
	for _, name := range closedSchemas {
		schema := contractMap(t, schemas[name], "components.schemas."+name)
		if schema["additionalProperties"] != false {
			t.Errorf("critical administrator schema %s is not closed", name)
		}
		assertNoOpenAdditionalProperties(t, schema, "components.schemas."+name)
	}
	for _, name := range []string{
		"BooleanControlEnvelope",
		"ServicePrincipalEnvelope",
		"IssuedCredentialEnvelope",
		"AgentPolicyEnvelope",
		"AgentPolicyListEnvelope",
		"AdminOverviewEnvelope",
		"CredentialRevocationEnvelope",
		"PolicyDisableEnvelope",
		"AdminTicketLeaseEnvelope",
		"AttachmentScanEnvelope",
		"ReplayEnvelope",
	} {
		schema := contractMap(t, schemas[name], "components.schemas."+name)
		assertNoOpenAdditionalProperties(t, schema, "components.schemas."+name)
	}
	policyConditions := contractMap(
		t,
		schemas["AgentPolicyConditions"],
		"components.schemas.AgentPolicyConditions",
	)
	conditionValues := contractMap(
		t,
		policyConditions["additionalProperties"],
		"AgentPolicyConditions.additionalProperties",
	)
	if got := conditionValues["$ref"]; got != "#/components/schemas/PolicyConditionValue" {
		t.Errorf("AgentPolicyConditions values = %v", got)
	}
	if _, exists := schemas["ActionResultEnvelope"]; exists {
		t.Error("open-ended ActionResultEnvelope still exists")
	}
	overviewEnvelope := contractMap(t, schemas["AdminOverviewEnvelope"], "components.schemas.AdminOverviewEnvelope")
	overviewEnvelopeAllOf, ok := overviewEnvelope["allOf"].([]any)
	if !ok || len(overviewEnvelopeAllOf) < 2 {
		t.Fatal("AdminOverviewEnvelope does not compose an envelope")
	}
	overviewEnvelopeExtension := contractMap(
		t,
		overviewEnvelopeAllOf[len(overviewEnvelopeAllOf)-1],
		"AdminOverviewEnvelope extension",
	)
	overviewEnvelopeProperties := contractMap(
		t,
		overviewEnvelopeExtension["properties"],
		"AdminOverviewEnvelope properties",
	)
	overviewEnvelopeData := contractMap(t, overviewEnvelopeProperties["data"], "AdminOverviewEnvelope data")
	if got := overviewEnvelopeData["$ref"]; got != "#/components/schemas/AdminOverview" {
		t.Errorf("AdminOverviewEnvelope data = %v", got)
	}

	versionedFields := map[string][]string{
		"AdminOverview": {
			"global_read_only_version",
			"emergency_stop_version",
		},
		"AdminServicePrincipalSummary": {"resource_version"},
		"AdminTicketLeaseSummary":      {"ticket_version", "resource_version"},
		"AdminDomainEventSummary":      {"resource_version"},
		"AdminOutboxDeliverySummary":   {"resource_version"},
		"AdminAttachmentSummary":       {"resource_version"},
		"AdminAgentPolicy":             {"resource_version"},
	}
	for schemaName, fields := range versionedFields {
		schema := contractMap(t, schemas[schemaName], "components.schemas."+schemaName)
		properties := contractMap(t, schema["properties"], "components.schemas."+schemaName+".properties")
		for _, field := range fields {
			if !contractSliceContains(schema["required"], field) {
				t.Errorf("%s does not require %s", schemaName, field)
			}
			property := contractMap(t, properties[field], schemaName+"."+field)
			if got := property["$ref"]; got != "#/components/schemas/ResourceVersion" {
				t.Errorf("%s.%s = %v, want ResourceVersion", schemaName, field, got)
			}
		}
	}

	overview := contractMap(t, schemas["AdminOverview"], "components.schemas.AdminOverview")
	overviewProperties := contractMap(t, overview["properties"], "AdminOverview.properties")
	overviewCollections := map[string]string{
		"principals":       "AdminServicePrincipalSummary",
		"leases":           "AdminTicketLeaseSummary",
		"events":           "AdminDomainEventSummary",
		"outbox":           "AdminOutboxDeliverySummary",
		"attachments":      "AdminAttachmentSummary",
		"policy_decisions": "AdminPolicyDecisionSummary",
	}
	for field, itemSchema := range overviewCollections {
		if !contractSliceContains(overview["required"], field) {
			t.Errorf("AdminOverview does not require %s", field)
		}
		collection := contractMap(t, overviewProperties[field], "AdminOverview."+field)
		items := contractMap(t, collection["items"], "AdminOverview."+field+".items")
		want := "#/components/schemas/" + itemSchema
		if got := items["$ref"]; got != want {
			t.Errorf("AdminOverview.%s items = %v, want %s", field, got, want)
		}
	}

	policyList := contractMap(t, schemas["AgentPolicyListEnvelope"], "components.schemas.AgentPolicyListEnvelope")
	policyListAllOf, ok := policyList["allOf"].([]any)
	if !ok || len(policyListAllOf) < 2 {
		t.Fatal("AgentPolicyListEnvelope does not compose an envelope")
	}
	policyListExtension := contractMap(t, policyListAllOf[len(policyListAllOf)-1], "AgentPolicyListEnvelope extension")
	policyListProperties := contractMap(t, policyListExtension["properties"], "AgentPolicyListEnvelope properties")
	policyListData := contractMap(t, policyListProperties["data"], "AgentPolicyListEnvelope data")
	policyListItems := contractMap(t, policyListData["items"], "AgentPolicyListEnvelope items")
	if got := policyListItems["$ref"]; got != "#/components/schemas/AdminAgentPolicy" {
		t.Errorf("policy list item schema = %v", got)
	}
}

func assertA2A10Contract(
	t *testing.T,
	paths map[string]any,
	components map[string]any,
	schemas map[string]any,
) {
	t.Helper()

	parameters := contractMap(t, components["parameters"], "components.parameters")
	versionHeader := contractMap(t, parameters["A2AVersion"], "components.parameters.A2AVersion")
	assertHeaderParameter(t, versionHeader, "A2AVersion", "A2A-Version", false)
	versionHeaderSchema := contractMap(t, versionHeader["schema"], "components.parameters.A2AVersion.schema")
	if got := versionHeaderSchema["const"]; got != "1.0" {
		t.Errorf("A2AVersion const = %v, want 1.0", got)
	}
	versionQuery := contractMap(t, parameters["A2AVersionQuery"], "components.parameters.A2AVersionQuery")
	if versionQuery["name"] != "A2A-Version" ||
		versionQuery["in"] != "query" ||
		versionQuery["required"] != false {
		t.Errorf("A2AVersionQuery = %#v, want optional A2A-Version query parameter", versionQuery)
	}
	versionQuerySchema := contractMap(t, versionQuery["schema"], "components.parameters.A2AVersionQuery.schema")
	if got := versionQuerySchema["const"]; got != "1.0" {
		t.Errorf("A2AVersionQuery const = %v, want 1.0", got)
	}
	post := contractOperation(t, paths, "/a2a/v1", "post")
	assertHeaderRefs(t, paths, "/a2a/v1", "post", "A2AVersion", "A2AVersionQuery")
	if got := post["operationId"]; got != "sendA2ARequest" {
		t.Errorf("POST /a2a/v1 operationId = %v", got)
	}
	requestBody := contractMap(t, post["requestBody"], "POST /a2a/v1 requestBody")
	requestContent := contractMap(t, requestBody["content"], "POST /a2a/v1 request content")
	requestJSON := contractMap(t, requestContent["application/json"], "POST /a2a/v1 JSON request")
	requestSchema := contractMap(t, requestJSON["schema"], "POST /a2a/v1 request schema")
	if got := requestSchema["$ref"]; got != "#/components/schemas/JSONRPCRequest" {
		t.Errorf("POST /a2a/v1 request schema = %v", got)
	}

	card := contractMap(t, schemas["A2AAgentCard"], "components.schemas.A2AAgentCard")
	cardProperties := contractMap(t, card["properties"], "A2AAgentCard.properties")
	interfaces := contractMap(t, cardProperties["supportedInterfaces"], "A2AAgentCard.supportedInterfaces")
	interfaceItems := contractMap(t, interfaces["items"], "A2AAgentCard.supportedInterfaces.items")
	interfaceProperties := contractMap(t, interfaceItems["properties"], "A2A interface properties")
	protocolVersion := contractMap(t, interfaceProperties["protocolVersion"], "A2A protocolVersion")
	if got := protocolVersion["const"]; got != "1.0" {
		t.Errorf("A2A Agent Card protocol version = %v, want 1.0", got)
	}

	if _, exists := paths["/a2a"]; exists {
		t.Error("unversioned legacy /a2a path is still published")
	}
	serialized := string(Specification())
	for _, forbidden := range []string{"0.3", "0.2", "A2A-Version-Override"} {
		if strings.Contains(serialized, forbidden) {
			t.Errorf("OpenAPI still contains legacy A2A contract %q", forbidden)
		}
	}
}

func assertUniqueYAMLMappingKeys(t *testing.T, node *yaml.Node, location string) {
	t.Helper()
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			key := node.Content[index]
			child := node.Content[index+1]
			identity := key.Tag + "\x00" + key.Value
			if _, duplicate := seen[identity]; duplicate {
				t.Errorf("duplicate YAML key %q at %s", key.Value, location)
			}
			seen[identity] = struct{}{}
			assertUniqueYAMLMappingKeys(t, child, location+"/"+key.Value)
		}
		return
	}
	for index, child := range node.Content {
		assertUniqueYAMLMappingKeys(t, child, fmt.Sprintf("%s/%d", location, index))
	}
}

func assertUniqueContractArrays(t *testing.T, value any, location string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childLocation := location + "/" + key
			if array, ok := child.([]any); ok && duplicateSensitiveArrayKey(key) {
				seen := make(map[string]int, len(array))
				for index, item := range array {
					encoded, err := json.Marshal(item)
					if err != nil {
						t.Fatalf("canonicalize %s/%d: %v", childLocation, index, err)
					}
					identity := string(encoded)
					if previous, duplicate := seen[identity]; duplicate {
						t.Errorf(
							"duplicate %s entry at %s/%d and %s/%d",
							key,
							childLocation,
							previous,
							childLocation,
							index,
						)
					}
					seen[identity] = index
				}
			}
			assertUniqueContractArrays(t, child, childLocation)
		}
	case []any:
		for index, child := range typed {
			assertUniqueContractArrays(t, child, fmt.Sprintf("%s/%d", location, index))
		}
	}
}

func duplicateSensitiveArrayKey(key string) bool {
	switch key {
	case "required", "enum", "allOf", "anyOf", "oneOf", "parameters", "security", "tags":
		return true
	default:
		return false
	}
}

func assertNoOpenAdditionalProperties(t *testing.T, value any, location string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childLocation := location + "/" + key
			if key == "additionalProperties" && child == true {
				t.Errorf("critical administrator schema is open at %s", childLocation)
			}
			assertNoOpenAdditionalProperties(t, child, childLocation)
		}
	case []any:
		for index, child := range typed {
			assertNoOpenAdditionalProperties(t, child, fmt.Sprintf("%s/%d", location, index))
		}
	}
}

func assertMCP20260728Contract(
	t *testing.T,
	paths map[string]any,
	components map[string]any,
	schemas map[string]any,
) {
	t.Helper()

	mcpPath := contractMap(t, paths["/mcp"], "paths./mcp")
	for _, forbiddenMethod := range []string{"get", "put", "delete", "patch", "options", "head", "trace"} {
		if _, exists := mcpPath[forbiddenMethod]; exists {
			t.Errorf("/mcp still exposes legacy %s transport", strings.ToUpper(forbiddenMethod))
		}
	}
	post := contractMap(t, mcpPath["post"], "POST /mcp")
	if post["operationId"] != "sendMCPRequest" {
		t.Errorf("POST /mcp operationId = %v", post["operationId"])
	}

	parameterRefs := make(map[string]bool)
	parameters, ok := post["parameters"].([]any)
	if !ok {
		t.Fatal("POST /mcp has no parameters")
	}
	for _, raw := range parameters {
		parameter := contractMap(t, raw, "POST /mcp parameter")
		ref, _ := parameter["$ref"].(string)
		parameterRefs[strings.TrimPrefix(ref, "#/components/parameters/")] = true
	}
	for _, name := range []string{"MCPAccept", "MCPProtocolVersion", "MCPMethod", "MCPName"} {
		if !parameterRefs[name] {
			t.Errorf("POST /mcp does not declare %s", name)
		}
	}
	for _, name := range []string{"MCPProtocolVersionRequired", "MCPSessionId", "MCPSessionIdOptional"} {
		if parameterRefs[name] {
			t.Errorf("POST /mcp still declares legacy parameter %s", name)
		}
	}

	componentParameters := contractMap(t, components["parameters"], "components.parameters")
	assertRequiredHeader(t, componentParameters, "MCPAccept", "Accept")
	mcpAccept := contractMap(t, componentParameters["MCPAccept"], "components.parameters.MCPAccept")
	mcpAcceptContract := fmt.Sprint(mcpAccept["description"], mcpAccept["example"])
	for _, mediaType := range []string{"application/json", "text/event-stream"} {
		if !strings.Contains(mcpAcceptContract, mediaType) {
			t.Errorf("MCPAccept does not require %s", mediaType)
		}
	}
	if !strings.Contains(mcpAcceptContract, "every") {
		t.Error("MCPAccept does not document that the dual-media Accept contract applies to every POST")
	}
	assertRequiredHeaderConst(t, componentParameters, "MCPProtocolVersion", "MCP-Protocol-Version", "2026-07-28")
	assertRequiredHeader(t, componentParameters, "MCPMethod", "Mcp-Method")
	mcpName := contractMap(t, componentParameters["MCPName"], "components.parameters.MCPName")
	if mcpName["name"] != "Mcp-Name" || mcpName["in"] != "header" || mcpName["required"] != false {
		t.Errorf("MCPName = %#v, want optional Mcp-Name header", mcpName)
	}
	mcpNameDescription, _ := mcpName["description"].(string)
	for _, binding := range []string{"tools/call", "params.name", "resources/read", "params.uri"} {
		if !strings.Contains(mcpNameDescription, binding) {
			t.Errorf("MCPName does not document %s binding", binding)
		}
	}
	mcpNameSchema := contractMap(t, mcpName["schema"], "components.parameters.MCPName.schema")
	if got := mcpNameSchema["maxLength"]; got != 4096 {
		t.Errorf("MCPName maxLength = %v, want 4096 for resource URIs", got)
	}
	for _, removed := range []string{"MCPProtocolVersionRequired", "MCPSessionId", "MCPSessionIdOptional"} {
		if _, exists := componentParameters[removed]; exists {
			t.Errorf("legacy MCP parameter component %s still exists", removed)
		}
	}

	requestBody := contractMap(t, post["requestBody"], "POST /mcp requestBody")
	requestContent := contractMap(t, requestBody["content"], "POST /mcp request content")
	jsonRequest := contractMap(t, requestContent["application/json"], "POST /mcp JSON request")
	requestSchema := contractMap(t, jsonRequest["schema"], "POST /mcp request schema")
	if got := requestSchema["$ref"]; got != "#/components/schemas/MCP20260728Request" {
		t.Errorf("POST /mcp request schema = %v", got)
	}
	requestExamples := contractMap(t, jsonRequest["examples"], "POST /mcp request examples")
	for name, rawExample := range requestExamples {
		example := contractMap(t, rawExample, "POST /mcp request example "+name)
		value := contractMap(t, example["value"], "POST /mcp request example value "+name)
		params := contractMap(t, value["params"], "POST /mcp request example params "+name)
		meta := contractMap(t, params["_meta"], "POST /mcp request example _meta "+name)
		clientCapabilities := contractMap(
			t,
			meta["io.modelcontextprotocol/clientCapabilities"],
			"POST /mcp request example clientCapabilities "+name,
		)
		extensions := contractMap(t, clientCapabilities["extensions"], "POST /mcp request example extensions "+name)
		settings := contractMap(
			t,
			extensions["io.modelcontextprotocol/oauth-client-credentials"],
			"POST /mcp OAuth client credentials extension settings "+name,
		)
		if len(settings) != 0 {
			t.Errorf("POST /mcp request example %s OAuth client credentials settings = %#v, want {}", name, settings)
		}
	}
	for name, methodName := range map[string]string{
		"callTool":     "tools/call",
		"readResource": "resources/read",
	} {
		example := contractMap(t, requestExamples[name], "POST /mcp request example "+name)
		value := contractMap(t, example["value"], "POST /mcp request example value "+name)
		if got := value["method"]; got != methodName {
			t.Errorf("POST /mcp request example %s method = %v, want %s", name, got, methodName)
		}
		exampleSummary := fmt.Sprint(example["summary"])
		if !strings.Contains(exampleSummary, "Mcp-Name") {
			t.Errorf("POST /mcp request example %s does not identify its Mcp-Name binding", name)
		}
	}
	if _, exists := requestExamples["cancelledNotification"]; exists {
		t.Error("POST /mcp must not advertise a client notification example")
	}

	modernRequest := contractMap(t, schemas["MCP20260728Request"], "components.schemas.MCP20260728Request")
	for _, field := range []string{"jsonrpc", "id", "method", "params"} {
		if !contractSliceContains(modernRequest["required"], field) {
			t.Errorf("MCP request does not require %s", field)
		}
	}
	callProperties := contractMap(t, modernRequest["properties"], "MCP20260728Request.properties")
	paramsRef := contractMap(t, callProperties["params"], "MCP20260728Request.params")
	if got := paramsRef["$ref"]; got != "#/components/schemas/MCP20260728CallParams" {
		t.Errorf("MCP call params schema = %v", got)
	}
	params := contractMap(t, schemas["MCP20260728CallParams"], "components.schemas.MCP20260728CallParams")
	if !contractSliceContains(params["required"], "_meta") {
		t.Error("MCP 2026-07-28 request does not require per-request _meta")
	}
	paramsProperties := contractMap(t, params["properties"], "MCP20260728Request.params.properties")
	notifications := contractMap(t, paramsProperties["notifications"], "MCP20260728Request.params.notifications")
	notificationProperties := contractMap(
		t,
		notifications["properties"],
		"MCP20260728Request.params.notifications.properties",
	)
	resourceSubscriptions := contractMap(
		t,
		notificationProperties["resourceSubscriptions"],
		"MCP20260728Request resourceSubscriptions",
	)
	if resourceSubscriptions["maxItems"] != 64 || resourceSubscriptions["uniqueItems"] != true {
		t.Errorf(
			"MCP resourceSubscriptions bounds = maxItems:%v uniqueItems:%v, want 64/true",
			resourceSubscriptions["maxItems"],
			resourceSubscriptions["uniqueItems"],
		)
	}
	method := contractMap(t, callProperties["method"], "MCP20260728Request.method")
	for _, methodName := range []string{
		"server/discover",
		"tools/call",
		"resources/read",
		"subscriptions/listen",
	} {
		if !contractSliceContains(method["enum"], methodName) {
			t.Errorf("MCP method enum does not include %s", methodName)
		}
	}
	if contractSliceContains(method["enum"], "notifications/cancelled") {
		t.Error("MCP call request must not include notifications/cancelled")
	}

	for _, removedSchema := range []string{
		"MCP20260728CallRequest",
		"MCP20260728CancelledNotification",
	} {
		if _, exists := schemas[removedSchema]; exists {
			t.Errorf("removed MCP compatibility schema %s still exists", removedSchema)
		}
	}

	meta := contractMap(t, schemas["MCPRequestMeta"], "components.schemas.MCPRequestMeta")
	for _, field := range []string{
		"io.modelcontextprotocol/protocolVersion",
		"io.modelcontextprotocol/clientCapabilities",
	} {
		if !contractSliceContains(meta["required"], field) {
			t.Errorf("MCP request _meta does not require %s", field)
		}
	}
	metaProperties := contractMap(t, meta["properties"], "MCPRequestMeta.properties")
	protocolVersion := contractMap(
		t,
		metaProperties["io.modelcontextprotocol/protocolVersion"],
		"MCPRequestMeta protocol version",
	)
	if got := protocolVersion["const"]; got != "2026-07-28" {
		t.Errorf("MCP request _meta protocol version = %v", got)
	}
	clientCapabilities := contractMap(
		t,
		metaProperties["io.modelcontextprotocol/clientCapabilities"],
		"MCPRequestMeta client capabilities",
	)
	clientCapabilityProperties := contractMap(
		t,
		clientCapabilities["properties"],
		"MCPRequestMeta client capability properties",
	)
	extensionContainer := contractMap(
		t,
		clientCapabilityProperties["extensions"],
		"MCPRequestMeta extensions",
	)
	extensionProperties := contractMap(
		t,
		extensionContainer["properties"],
		"MCPRequestMeta extension properties",
	)
	oauthExtension := contractMap(
		t,
		extensionProperties["io.modelcontextprotocol/oauth-client-credentials"],
		"MCP OAuth client credentials extension",
	)
	if oauthExtension["type"] != "object" || oauthExtension["additionalProperties"] != false {
		t.Errorf("MCP OAuth client credentials extension schema = %#v, want closed object", oauthExtension)
	}

	responses := contractMap(t, post["responses"], "POST /mcp responses")
	discoverOKResponse := contractMap(t, responses["200"], "POST /mcp 200 response")
	discoverOKContent := contractMap(t, discoverOKResponse["content"], "POST /mcp 200 response content")
	discoverOKJSON := contractMap(t, discoverOKContent["application/json"], "POST /mcp 200 JSON response")
	discoverResponse := contractMap(t, discoverOKJSON["example"], "POST /mcp discover response example")
	discoverResult := contractMap(t, discoverResponse["result"], "POST /mcp discover response result")
	serverCapabilities := contractMap(t, discoverResult["capabilities"], "POST /mcp discover server capabilities")
	serverExtensions := contractMap(t, serverCapabilities["extensions"], "POST /mcp discover server extensions")
	serverOAuthSettings := contractMap(
		t,
		serverExtensions["io.modelcontextprotocol/oauth-client-credentials"],
		"POST /mcp discover OAuth client credentials extension settings",
	)
	if len(serverOAuthSettings) != 0 {
		t.Errorf("MCP discover OAuth client credentials settings = %#v, want {}", serverOAuthSettings)
	}
	for _, status := range []string{"200", "400", "401", "403", "404", "405", "413", "415", "429"} {
		if _, exists := responses[status]; !exists {
			t.Errorf("POST /mcp response %s is missing", status)
		}
	}
	for _, legacyStatus := range []string{"202", "204"} {
		if _, exists := responses[legacyStatus]; exists {
			t.Errorf("POST /mcp still declares legacy response %s", legacyStatus)
		}
	}
	badRequest := contractMap(t, responses["400"], "POST /mcp response 400")
	badRequestDescription := fmt.Sprint(badRequest["description"])
	for _, distinction := range []string{"official Go SDK", "text/plain", "application/json", "no JSON-RPC envelope", "client-to-server notifications"} {
		if !strings.Contains(badRequestDescription, distinction) {
			t.Errorf("POST /mcp response 400 does not document %q distinction", distinction)
		}
	}
	badRequestContent := contractMap(t, badRequest["content"], "POST /mcp response 400 content")
	badRequestJSON := contractMap(t, badRequestContent["application/json"], "POST /mcp response 400 JSON")
	badRequestExamples := contractMap(t, badRequestJSON["examples"], "POST /mcp response 400 examples")
	for name, code := range map[string]int{
		"missingHeader":             -32020,
		"missingClientCapabilities": -32021,
		"unsupportedVersion":        -32022,
		"clientNotification":        -32600,
	} {
		example := contractMap(t, badRequestExamples[name], "POST /mcp response 400 example "+name)
		value := contractMap(t, example["value"], "POST /mcp response 400 example value "+name)
		rpcError := contractMap(t, value["error"], "POST /mcp response 400 error "+name)
		if got := rpcError["code"]; got != code {
			t.Errorf("POST /mcp response 400 example %s code = %v, want %d", name, got, code)
		}
	}
	badRequestText := contractMap(t, badRequestContent["text/plain"], "POST /mcp response 400 text")
	badRequestTextSchema := contractMap(t, badRequestText["schema"], "POST /mcp response 400 text schema")
	if got := badRequestTextSchema["type"]; got != "string" {
		t.Errorf("POST /mcp text 400 schema type = %v, want string", got)
	}
	badRequestTextExamples := contractMap(t, badRequestText["examples"], "POST /mcp response 400 text examples")
	for name, requiredText := range map[string]string{
		"missingAccept": "Accept must contain both",
		"malformedBody": "malformed payload",
	} {
		example := contractMap(t, badRequestTextExamples[name], "POST /mcp text 400 example "+name)
		if !strings.Contains(fmt.Sprint(example["value"]), requiredText) {
			t.Errorf("POST /mcp text 400 example %s does not contain %q", name, requiredText)
		}
	}
	tooMany := contractMap(t, responses["429"], "POST /mcp response 429")
	tooManyContent := contractMap(t, tooMany["content"], "POST /mcp response 429 content")
	tooManyJSON := contractMap(t, tooManyContent["application/json"], "POST /mcp response 429 JSON")
	tooManyExample := contractMap(t, tooManyJSON["example"], "POST /mcp response 429 example")
	tooManyError := contractMap(t, tooManyExample["error"], "POST /mcp response 429 error")
	tooManyData := contractMap(t, tooManyError["data"], "POST /mcp response 429 data")
	if tooManyData["code"] != "subscription_limit_exceeded" ||
		tooManyData["limit_scope"] != "credential" ||
		tooManyData["limit"] != 4 {
		t.Errorf("POST /mcp response 429 example = %#v", tooManyExample)
	}

	okResponse := contractMap(t, responses["200"], "POST /mcp response 200")
	okContent := contractMap(t, okResponse["content"], "POST /mcp response 200 content")
	jsonResponse := contractMap(t, okContent["application/json"], "POST /mcp JSON response")
	jsonResponseSchema := contractMap(t, jsonResponse["schema"], "POST /mcp JSON response schema")
	if got := jsonResponseSchema["$ref"]; got != "#/components/schemas/MCP20260728Response" {
		t.Errorf("POST /mcp JSON response schema = %v", got)
	}
	sseResponse := contractMap(t, okContent["text/event-stream"], "POST /mcp SSE response")
	sseSchema := contractMap(t, sseResponse["schema"], "POST /mcp SSE response schema")
	if got := sseSchema["$ref"]; got != "#/components/schemas/MCPSSEStream" {
		t.Errorf("POST /mcp SSE response schema = %v", got)
	}

	modernResult := contractMap(t, schemas["MCPModernResult"], "components.schemas.MCPModernResult")
	if !contractSliceContains(modernResult["required"], "resultType") {
		t.Error("MCP modern result does not require resultType")
	}
	resultProperties := contractMap(t, modernResult["properties"], "MCPModernResult.properties")
	for _, field := range []string{"resultType", "ttlMs", "cacheScope"} {
		if _, exists := resultProperties[field]; !exists {
			t.Errorf("MCP modern result does not declare %s", field)
		}
	}

	unauthorized := contractMap(t, responses["401"], "POST /mcp response 401")
	if got := unauthorized["$ref"]; got != "#/components/responses/MCPUnauthorized" {
		t.Errorf("POST /mcp 401 response = %v", got)
	}
	componentResponses := contractMap(t, components["responses"], "components.responses")
	mcpUnauthorized := contractMap(t, componentResponses["MCPUnauthorized"], "components.responses.MCPUnauthorized")
	unauthorizedHeaders := contractMap(t, mcpUnauthorized["headers"], "MCPUnauthorized.headers")
	challenge := contractMap(t, unauthorizedHeaders["WWW-Authenticate"], "MCPUnauthorized WWW-Authenticate")
	if got := challenge["$ref"]; got != "#/components/headers/MCPBearerChallenge" {
		t.Errorf("MCP OAuth challenge = %v", got)
	}
	componentHeaders := contractMap(t, components["headers"], "components.headers")
	bearerChallenge := contractMap(t, componentHeaders["MCPBearerChallenge"], "components.headers.MCPBearerChallenge")
	challengeText := fmt.Sprint(bearerChallenge["description"], bearerChallenge["example"])
	for _, requiredText := range []string{"resource_metadata", "scope"} {
		if !strings.Contains(challengeText, requiredText) {
			t.Errorf("MCP Bearer challenge does not document %s", requiredText)
		}
	}

	forbiddenResponse := contractMap(t, responses["403"], "POST /mcp response 403")
	forbiddenContent := contractMap(t, forbiddenResponse["content"], "POST /mcp response 403 content")
	forbiddenJSON := contractMap(t, forbiddenContent["application/json"], "POST /mcp response 403 JSON")
	forbiddenExamples := contractMap(t, forbiddenJSON["examples"], "POST /mcp response 403 examples")
	for name, reasonCode := range map[string]string{
		"insufficientScope": "insufficient_scope",
		"policyDenied":      "policy_denied",
	} {
		example := contractMap(t, forbiddenExamples[name], "POST /mcp response 403 example "+name)
		value := contractMap(t, example["value"], "POST /mcp response 403 example value "+name)
		rpcError := contractMap(t, value["error"], "POST /mcp response 403 error "+name)
		if got := rpcError["code"]; got != -32600 {
			t.Errorf("POST /mcp response 403 example %s code = %v, want -32600", name, got)
		}
		data := contractMap(t, rpcError["data"], "POST /mcp response 403 data "+name)
		if got := data["code"]; got != reasonCode {
			t.Errorf("POST /mcp response 403 example %s data.code = %v, want %s", name, got, reasonCode)
		}
	}

	serialized := string(Specification())
	for _, forbidden := range []string{
		"2024-11-05",
		"2025-03-26",
		"2025-06-18",
		"2025-11-25",
		"2026-03-26",
		"Mcp-Session-Id",
		"MCPSessionId",
		"terminateMCPSession",
		"streamMCPResourceNotifications",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Errorf("OpenAPI still contains legacy MCP contract %q", forbidden)
		}
	}

	// Last-Event-ID remains valid for the independent A2A 1.0 SSE contract,
	// but it must never appear on the stateless MCP operation.
	if strings.Contains(fmt.Sprint(post), "Last-Event-ID") {
		t.Error("POST /mcp still exposes a replay cursor")
	}
}

func assertRequiredHeaderConst(
	t *testing.T,
	parameters map[string]any,
	componentName string,
	headerName string,
	value string,
) {
	t.Helper()
	parameter := contractMap(t, parameters[componentName], "components.parameters."+componentName)
	assertHeaderParameter(t, parameter, componentName, headerName, true)
	schema := contractMap(t, parameter["schema"], "components.parameters."+componentName+".schema")
	if got := schema["const"]; got != value {
		t.Errorf("%s const = %v, want %s", componentName, got, value)
	}
}

func assertRequiredHeader(
	t *testing.T,
	parameters map[string]any,
	componentName string,
	headerName string,
) {
	t.Helper()
	parameter := contractMap(t, parameters[componentName], "components.parameters."+componentName)
	assertHeaderParameter(t, parameter, componentName, headerName, true)
}

func assertHeaderParameter(
	t *testing.T,
	parameter map[string]any,
	componentName string,
	headerName string,
	required bool,
) {
	t.Helper()
	if parameter["name"] != headerName || parameter["in"] != "header" || parameter["required"] != required {
		t.Errorf(
			"%s = %#v, want required=%t header %s",
			componentName,
			parameter,
			required,
			headerName,
		)
	}
}

func contractSliceContains(value any, want string) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func assertMCPResourceBoundTokenContract(
	t *testing.T,
	paths map[string]any,
	schemas map[string]any,
) {
	t.Helper()

	if _, exists := paths["/.well-known/oauth-protected-resource"]; exists {
		t.Error("non-canonical protected-resource metadata alias is still exposed")
	}
	for path, operationID := range map[string]string{
		"/.well-known/oauth-protected-resource/mcp":    "getMCPOAuthProtectedResourceMetadata",
		"/.well-known/oauth-protected-resource/api/v1": "getAPIOAuthProtectedResourceMetadata",
		"/.well-known/oauth-protected-resource/a2a/v1": "getA2AOAuthProtectedResourceMetadata",
	} {
		operation := contractOperation(t, paths, path, "get")
		if got := operation["operationId"]; got != operationID {
			t.Errorf("GET %s operationId = %v, want %s", path, got, operationID)
		}
	}
	protectedMetadata := contractMap(
		t,
		schemas["ProtectedResourceMetadata"],
		"components.schemas.ProtectedResourceMetadata",
	)
	protectedProperties := contractMap(
		t,
		protectedMetadata["properties"],
		"ProtectedResourceMetadata.properties",
	)
	if _, exists := protectedProperties["resource_name"]; !exists {
		t.Error("ProtectedResourceMetadata does not declare runtime resource_name")
	}
	authorizationMetadata := contractMap(
		t,
		schemas["AuthorizationServerMetadata"],
		"components.schemas.AuthorizationServerMetadata",
	)
	authorizationProperties := contractMap(
		t,
		authorizationMetadata["properties"],
		"AuthorizationServerMetadata.properties",
	)
	for _, field := range []string{
		"client_id_metadata_document_supported",
		"authorization_response_iss_parameter_supported",
	} {
		if _, exists := authorizationProperties[field]; !exists {
			t.Errorf("AuthorizationServerMetadata does not declare runtime %s", field)
		}
	}

	capabilities := contractOperation(t, paths, "/capabilities", "get")
	capabilityResponses := contractMap(t, capabilities["responses"], "GET /capabilities responses")
	capabilityOK := contractMap(t, capabilityResponses["200"], "GET /capabilities response 200")
	capabilityContent := contractMap(t, capabilityOK["content"], "GET /capabilities response content")
	capabilityJSON := contractMap(t, capabilityContent["application/json"], "GET /capabilities JSON response")
	capabilityExample := contractMap(t, capabilityJSON["example"], "GET /capabilities example")
	capabilityData := contractMap(t, capabilityExample["data"], "GET /capabilities example data")
	oauthMetadata := contractMap(t, capabilityData["oauth_metadata"], "GET /capabilities oauth_metadata")
	for resource, path := range map[string]string{
		"api": "/.well-known/oauth-protected-resource/api/v1",
		"mcp": "/.well-known/oauth-protected-resource/mcp",
		"a2a": "/.well-known/oauth-protected-resource/a2a/v1",
	} {
		if got := oauthMetadata[resource]; got != path {
			t.Errorf("GET /capabilities oauth_metadata.%s = %v, want %s", resource, got, path)
		}
	}

	tokenOperation := contractOperation(t, paths, "/oauth/token", "post")
	requestBody := contractMap(t, tokenOperation["requestBody"], "POST /oauth/token requestBody")
	content := contractMap(t, requestBody["content"], "POST /oauth/token request content")
	if len(content) != 1 {
		t.Errorf("POST /oauth/token content types = %d, want form-only contract", len(content))
	}
	if _, exists := content["application/json"]; exists {
		t.Error("POST /oauth/token still accepts legacy JSON credentials")
	}
	form := contractMap(
		t,
		content["application/x-www-form-urlencoded"],
		"POST /oauth/token form request",
	)
	formSchema := contractMap(t, form["schema"], "POST /oauth/token form schema")
	if got := formSchema["$ref"]; got != "#/components/schemas/OAuthTokenRequest" {
		t.Errorf("POST /oauth/token form schema = %v", got)
	}
	formExamples := contractMap(t, form["examples"], "POST /oauth/token form examples")
	for name, expectedResource := range map[string]string{
		"mcp": "https://chronodesk.example/mcp",
		"api": "https://chronodesk.example/api/v1",
		"a2a": "https://chronodesk.example/a2a/v1",
	} {
		example := contractMap(t, formExamples[name], "POST /oauth/token "+name+" example")
		value := contractMap(t, example["value"], "POST /oauth/token "+name+" example value")
		if got := value["resource"]; got != expectedResource {
			t.Errorf("POST /oauth/token %s example resource = %v, want %s", name, got, expectedResource)
		}
	}

	tokenRequest := contractMap(t, schemas["OAuthTokenRequest"], "components.schemas.OAuthTokenRequest")
	for _, field := range []string{"grant_type", "resource"} {
		if !contractSliceContains(tokenRequest["required"], field) {
			t.Errorf("OAuthTokenRequest does not require %s", field)
		}
	}
	requestProperties := contractMap(t, tokenRequest["properties"], "OAuthTokenRequest.properties")
	resource := contractMap(t, requestProperties["resource"], "OAuthTokenRequest.resource")
	if got := resource["format"]; got != "uri" {
		t.Errorf("OAuthTokenRequest.resource format = %v, want uri", got)
	}
	resourceDescription := fmt.Sprint(resource["description"])
	for _, audience := range []string{"${APP_URL}/mcp", "${APP_URL}/api/v1", "${APP_URL}/a2a/v1"} {
		if !strings.Contains(resourceDescription, audience) {
			t.Errorf("OAuthTokenRequest.resource does not document canonical audience %s", audience)
		}
	}

	oauthError := contractMap(t, schemas["OAuthError"], "components.schemas.OAuthError")
	errorProperties := contractMap(t, oauthError["properties"], "OAuthError.properties")
	errorCode := contractMap(t, errorProperties["error"], "OAuthError.error")
	for _, code := range []string{"invalid_request", "invalid_target"} {
		if !contractSliceContains(errorCode["enum"], code) {
			t.Errorf("OAuthError does not declare %s", code)
		}
	}

	responses := contractMap(t, tokenOperation["responses"], "POST /oauth/token responses")
	badRequest := contractMap(t, responses["400"], "POST /oauth/token response 400")
	badRequestContent := contractMap(t, badRequest["content"], "POST /oauth/token response 400 content")
	badRequestJSON := contractMap(t, badRequestContent["application/json"], "POST /oauth/token response 400 JSON")
	examples := contractMap(t, badRequestJSON["examples"], "POST /oauth/token response 400 examples")
	invalidRequest := contractMap(t, examples["invalidRequest"], "POST /oauth/token invalidRequest example")
	invalidRequestValue := contractMap(t, invalidRequest["value"], "POST /oauth/token invalidRequest value")
	if got := invalidRequestValue["error"]; got != "invalid_request" {
		t.Errorf("POST /oauth/token invalidRequest error = %v", got)
	}
	invalidTarget := contractMap(t, examples["invalidTarget"], "POST /oauth/token invalidTarget example")
	value := contractMap(t, invalidTarget["value"], "POST /oauth/token invalidTarget value")
	if got := value["error"]; got != "invalid_target" {
		t.Errorf("POST /oauth/token invalidTarget error = %v", got)
	}
	if _, exists := responses["429"]; !exists {
		t.Error("POST /oauth/token does not document its anonymous rate limit")
	}
}

func assertOperationResponseSchema(
	t *testing.T,
	paths map[string]any,
	path string,
	method string,
	status string,
	schemaName string,
) {
	t.Helper()
	operation := contractOperation(t, paths, path, method)
	responses := contractMap(t, operation["responses"], strings.ToUpper(method)+" "+path+" responses")
	response := contractMap(t, responses[status], strings.ToUpper(method)+" "+path+" response "+status)
	content := contractMap(t, response["content"], strings.ToUpper(method)+" "+path+" response content")
	mediaType := contractMap(t, content["application/json"], strings.ToUpper(method)+" "+path+" JSON response")
	schema := contractMap(t, mediaType["schema"], strings.ToUpper(method)+" "+path+" response schema")
	want := "#/components/schemas/" + schemaName
	if got := schema["$ref"]; got != want {
		t.Errorf("%s %s response %s schema = %v, want %s", strings.ToUpper(method), path, status, got, want)
	}
}

func assertOperationResponseRef(
	t *testing.T,
	paths map[string]any,
	path string,
	method string,
	status string,
	responseName string,
) {
	t.Helper()
	operation := contractOperation(t, paths, path, method)
	responses := contractMap(t, operation["responses"], strings.ToUpper(method)+" "+path+" responses")
	response := contractMap(t, responses[status], strings.ToUpper(method)+" "+path+" response "+status)
	want := "#/components/responses/" + responseName
	if got := response["$ref"]; got != want {
		t.Errorf("%s %s response %s = %v, want %s", strings.ToUpper(method), path, status, got, want)
	}
}

func assertResponseHeaderRef(
	t *testing.T,
	paths map[string]any,
	path string,
	method string,
	status string,
	headerName string,
	componentName string,
) {
	t.Helper()
	operation := contractOperation(t, paths, path, method)
	responses := contractMap(t, operation["responses"], strings.ToUpper(method)+" "+path+" responses")
	response := contractMap(t, responses[status], strings.ToUpper(method)+" "+path+" response "+status)
	headers := contractMap(t, response["headers"], strings.ToUpper(method)+" "+path+" response headers")
	header := contractMap(t, headers[headerName], strings.ToUpper(method)+" "+path+" response header "+headerName)
	want := "#/components/headers/" + componentName
	if got := header["$ref"]; got != want {
		t.Errorf("%s %s response header %s = %v, want %s", strings.ToUpper(method), path, headerName, got, want)
	}
}

func assertSchemaRequiresReceipt(t *testing.T, schemas map[string]any, schemaName string) {
	t.Helper()
	schema := contractMap(t, schemas[schemaName], "components.schemas."+schemaName)
	allOf, ok := schema["allOf"].([]any)
	if !ok || len(allOf) < 2 {
		t.Fatalf("components.schemas.%s does not compose an envelope", schemaName)
	}
	extension := contractMap(t, allOf[len(allOf)-1], "components.schemas."+schemaName+".allOf")
	required, ok := extension["required"].([]any)
	if !ok {
		t.Fatalf("components.schemas.%s does not require receipt", schemaName)
	}
	for _, field := range required {
		if field == "receipt" {
			return
		}
	}
	t.Fatalf("components.schemas.%s required fields = %v, want receipt", schemaName, required)
}

func assertNoStoreCredentialResponse(
	t *testing.T,
	paths map[string]any,
	path string,
	method string,
	status string,
) {
	t.Helper()
	operation := contractOperation(t, paths, path, method)
	responses := contractMap(t, operation["responses"], strings.ToUpper(method)+" "+path+" responses")
	response := contractMap(t, responses[status], strings.ToUpper(method)+" "+path+" response "+status)
	headers := contractMap(t, response["headers"], strings.ToUpper(method)+" "+path+" response headers")
	for name, value := range map[string]string{"Cache-Control": "no-store", "Pragma": "no-cache"} {
		header := contractMap(t, headers[name], strings.ToUpper(method)+" "+path+" "+name)
		schema := contractMap(t, header["schema"], strings.ToUpper(method)+" "+path+" "+name+" schema")
		if got := schema["const"]; got != value {
			t.Errorf("%s %s response header %s const = %v, want %s", strings.ToUpper(method), path, name, got, value)
		}
	}
}

func contractOperation(t *testing.T, paths map[string]any, path, method string) map[string]any {
	t.Helper()
	pathItem := contractMap(t, paths[path], "paths."+path)
	return contractMap(t, pathItem[method], strings.ToUpper(method)+" "+path)
}

func assertHeaderRefs(t *testing.T, paths map[string]any, path, method string, names ...string) {
	t.Helper()
	operation := contractOperation(t, paths, path, method)
	parameters, ok := operation["parameters"].([]any)
	if !ok {
		t.Fatalf("%s %s has no parameters", strings.ToUpper(method), path)
	}
	refs := make(map[string]bool)
	for _, raw := range parameters {
		parameter := contractMap(t, raw, strings.ToUpper(method)+" "+path+" parameter")
		ref, _ := parameter["$ref"].(string)
		refs[strings.TrimPrefix(ref, "#/components/parameters/")] = true
	}
	for _, name := range names {
		if !refs[name] {
			t.Errorf("%s %s does not declare %s", strings.ToUpper(method), path, name)
		}
	}
}

func hasOAuthScopeAlternative(operation map[string]any, requiredScope string) bool {
	security, ok := operation["security"].([]any)
	if !ok {
		return false
	}
	for _, rawRequirement := range security {
		requirement, ok := rawRequirement.(map[string]any)
		if !ok {
			continue
		}
		rawScopes, exists := requirement["oauth2"]
		if !exists {
			continue
		}
		scopes, ok := rawScopes.([]any)
		if !ok {
			continue
		}
		for _, rawScope := range scopes {
			if rawScope == requiredScope {
				return true
			}
		}
	}
	return false
}

func contractMap(t *testing.T, value any, location string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want object", location, value)
	}
	return result
}

func walkContract(value any, location string, visitRef func(location, ref string)) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childLocation := location + "/" + key
			if key == "$ref" {
				if ref, ok := child.(string); ok {
					visitRef(childLocation, ref)
				}
			}
			walkContract(child, childLocation, visitRef)
		}
	case []any:
		for index, child := range typed {
			walkContract(child, fmt.Sprintf("%s/%d", location, index), visitRef)
		}
	}
}

func resolveContractRef(document map[string]any, ref string) (any, bool) {
	current := any(document)
	for _, token := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[token]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

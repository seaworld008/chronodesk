package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

const (
	IntegrationHMACSHA256SignatureScheme = "hmac-sha256"

	maxIntegrationMappingDefinitionBytes = 256 << 10
	maxIntegrationMappingFields          = 64
	maxIntegrationJSONDepth              = 32
	maxIntegrationMappedBytes            = 512 << 10
)

var (
	ErrIntegrationVerificationKeyUnavailable = errors.New(
		"integration verification key is unavailable",
	)
	ErrIntegrationRuntimeInvalidMapping = errors.New(
		"integration declarative mapping is invalid",
	)
	ErrIntegrationRuntimeUnsupportedCommand = errors.New(
		"integration runtime command is not supported",
	)
	ErrIntegrationRuntimeScopeMismatch = errors.New(
		"integration runtime scope mismatch",
	)
)

// IntegrationVerificationKeySet is returned by a secret-store Adapter. Key
// bytes are never persisted by ChronoDesk and must never be serialized or
// logged. Previous is optional and exists only for bounded key rotation.
type IntegrationVerificationKeySet struct {
	Current  []byte `json:"-"`
	Previous []byte `json:"-"`
}

func (IntegrationVerificationKeySet) String() string {
	return "[REDACTED integration verification keys]"
}

// IntegrationVerificationKeyResolver resolves the opaque
// Connection.VerificationKeyRef against a composition-layer secret store.
// Implementations must not return the reference itself as key material.
type IntegrationVerificationKeyResolver interface {
	ResolveIntegrationVerificationKeys(
		context.Context,
		string,
	) (IntegrationVerificationKeySet, error)
}

type IntegrationVerificationKeyResolverFunc func(
	context.Context,
	string,
) (IntegrationVerificationKeySet, error)

func (function IntegrationVerificationKeyResolverFunc) ResolveIntegrationVerificationKeys(
	ctx context.Context,
	reference string,
) (IntegrationVerificationKeySet, error) {
	return function(ctx, reference)
}

// IntegrationHMACSHA256Verifier implements the one inbound Webhook signature
// contract. Every routing and idempotency value that can influence the domain
// command is authenticated together with the exact raw body.
type IntegrationHMACSHA256Verifier struct {
	keys IntegrationVerificationKeyResolver
}

func NewIntegrationHMACSHA256Verifier(
	keys IntegrationVerificationKeyResolver,
) (*IntegrationHMACSHA256Verifier, error) {
	if keys == nil {
		return nil, errors.New("integration verification key resolver is required")
	}
	return &IntegrationHMACSHA256Verifier{keys: keys}, nil
}

func (verifier *IntegrationHMACSHA256Verifier) Verify(
	ctx context.Context,
	input IntegrationSignatureVerification,
) error {
	if verifier == nil || verifier.keys == nil ||
		input.Connection == nil || input.Connector == nil ||
		input.SignedAt.IsZero() || len(input.Body) == 0 {
		return ErrIntegrationSignatureRejected
	}
	if !validIntegrationSignatureMetadata(input) {
		return ErrIntegrationSignatureRejected
	}
	if strings.TrimSpace(input.Connector.SignatureScheme) !=
		IntegrationHMACSHA256SignatureScheme {
		return ErrIntegrationSignatureRejected
	}
	reference := strings.TrimSpace(input.Connection.VerificationKeyRef)
	if reference == "" {
		return ErrIntegrationSignatureRejected
	}
	provided, err := parseIntegrationHMACSignature(input.Signature)
	if err != nil {
		return ErrIntegrationSignatureRejected
	}
	keys, err := verifier.keys.ResolveIntegrationVerificationKeys(ctx, reference)
	if err != nil {
		return ErrIntegrationVerificationKeyUnavailable
	}
	current := append([]byte(nil), keys.Current...)
	previous := append([]byte(nil), keys.Previous...)
	defer clear(current)
	defer clear(previous)
	if !validIntegrationVerificationKey(current) ||
		(len(previous) != 0 && !validIntegrationVerificationKey(previous)) {
		return ErrIntegrationVerificationKeyUnavailable
	}

	signed := integrationHMACSigningPayload(input)
	currentMAC := integrationHMAC(current, signed)
	currentMatches := hmac.Equal(provided, currentMAC)
	previousMatches := false
	if len(previous) != 0 {
		previousMAC := integrationHMAC(previous, signed)
		previousMatches = hmac.Equal(provided, previousMAC)
		clear(previousMAC)
	}
	clear(currentMAC)
	clear(signed)
	clear(provided)
	if !currentMatches && !previousMatches {
		return ErrIntegrationSignatureRejected
	}
	return nil
}

func validIntegrationSignatureMetadata(
	input IntegrationSignatureVerification,
) bool {
	values := []string{
		input.ProjectKey,
		input.Connection.PublicID,
		input.MappingPublicID,
		input.MessageID,
		input.ExternalResourceType,
		input.ExternalResourceID,
		input.ContentType,
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" ||
			strings.TrimSpace(value) != value ||
			strings.ContainsAny(value, "\r\n") {
			return false
		}
	}
	return models.ValidateProjectKey(input.ProjectKey) == nil &&
		canonicalIntegrationUUID(input.Connection.PublicID) &&
		canonicalIntegrationUUID(input.MappingPublicID)
}

// integrationHMACSigningPayload returns the byte-for-byte v1 signing input:
//
//	v1\n
//	timestamp\n
//	project_key\n
//	connection_public_id\n
//	mapping_public_id\n
//	message_id\n
//	external_resource_type\n
//	external_resource_id\n
//	content_type\n
//	exact_raw_body
//
// Every text component is validated to exclude CR/LF, making the framing
// unambiguous. This function deliberately does not include secret material.
func integrationHMACSigningPayload(
	input IntegrationSignatureVerification,
) []byte {
	timestamp := strconv.FormatInt(input.SignedAt.UTC().Unix(), 10)
	fields := []string{
		"v1",
		timestamp,
		input.ProjectKey,
		input.Connection.PublicID,
		input.MappingPublicID,
		input.MessageID,
		input.ExternalResourceType,
		input.ExternalResourceID,
		input.ContentType,
	}
	size := len(input.Body)
	for _, field := range fields {
		size += len(field) + 1
	}
	signed := make([]byte, 0, size)
	for _, field := range fields {
		signed = append(signed, field...)
		signed = append(signed, '\n')
	}
	return append(signed, input.Body...)
}

func validIntegrationVerificationKey(key []byte) bool {
	return len(key) >= 32 && len(key) <= 4096
}

func parseIntegrationHMACSignature(value string) ([]byte, error) {
	if len(value) != len("v1=")+sha256.Size*2 ||
		!strings.HasPrefix(value, "v1=") {
		return nil, ErrIntegrationSignatureRejected
	}
	encoded := value[len("v1="):]
	for _, character := range encoded {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return nil, ErrIntegrationSignatureRejected
		}
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size {
		return nil, ErrIntegrationSignatureRejected
	}
	return decoded, nil
}

func integrationHMAC(key, signed []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(signed)
	return mac.Sum(nil)
}

// IntegrationInboundTarget is resolved from opaque public identifiers before
// an inbound message enters the Inbox. It contains only trusted database IDs.
type IntegrationInboundTarget struct {
	Scope            models.ProjectScope
	ConnectionID     uint
	MappingVersionID uint
}

// IntegrationInboundTargetResolver is the narrow public-ID boundary consumed
// by the unauthenticated HTTP Adapter. Receive still performs the authoritative
// connection state, HMAC, replay-window and mapping checks.
type IntegrationInboundTargetResolver interface {
	ResolvePublicInboundTarget(
		context.Context,
		string,
		string,
		string,
	) (IntegrationInboundTarget, error)
}

// ResolvePublicInboundTarget resolves the single-organization project key and
// project-local UUIDv7 resources. Cross-project public IDs fail closed.
func (service *IntegrationInboxService) ResolvePublicInboundTarget(
	ctx context.Context,
	projectKey string,
	connectionPublicID string,
	mappingPublicID string,
) (IntegrationInboundTarget, error) {
	if service == nil || service.db == nil || ctx == nil ||
		models.ValidateProjectKey(projectKey) != nil ||
		!canonicalIntegrationUUID(connectionPublicID) ||
		!canonicalIntegrationUUID(mappingPublicID) {
		return IntegrationInboundTarget{}, ErrIntegrationInvalidInput
	}
	var projects []models.Project
	if err := service.db.WithContext(ctx).
		Where("key = ? AND status = ?", projectKey, models.ProjectStatusActive).
		Limit(2).
		Find(&projects).Error; err != nil {
		return IntegrationInboundTarget{}, ErrIntegrationProjectNotFound
	}
	// The first release is explicitly single-organization. If that invariant is
	// ever violated, a bare project key is ambiguous and must not pick a tenant.
	if len(projects) != 1 {
		return IntegrationInboundTarget{}, ErrIntegrationProjectNotFound
	}
	project := projects[0]
	var (
		connection models.Connection
		mapping    models.MappingVersion
		resolveErr error
	)
	err := scopeddb.WithProjectScopeTransaction(
		ctx,
		service.db,
		project.Scope(),
		func(tx *gorm.DB) error {
			if err := tx.Where(
				"public_id = ? AND organization_id = ? AND project_id = ? AND status = ?",
				connectionPublicID,
				project.OrganizationID,
				project.ID,
				models.ConnectionStatusActive,
			).First(&connection).Error; err != nil {
				resolveErr = ErrIntegrationConnectionNotFound
				return nil
			}
			if err := tx.Where(
				"public_id = ? AND organization_id = ? AND project_id = ? AND connection_id = ? AND status = ?",
				mappingPublicID,
				project.OrganizationID,
				project.ID,
				connection.ID,
				models.MappingVersionStatusPublished,
			).First(&mapping).Error; err != nil {
				resolveErr = ErrIntegrationMappingNotFound
			}
			return nil
		},
	)
	if err != nil {
		return IntegrationInboundTarget{}, err
	}
	if resolveErr != nil {
		return IntegrationInboundTarget{}, resolveErr
	}
	return IntegrationInboundTarget{
		Scope:            project.Scope(),
		ConnectionID:     connection.ID,
		MappingVersionID: mapping.ID,
	}, nil
}

func canonicalIntegrationUUID(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

// DeclarativeIntegrationRuntime is both the published-mapping dry runner and
// the production command Adapter. It deliberately implements only commands
// whose complete invariants can be delegated to AgentNativeService.
type DeclarativeIntegrationRuntime struct {
	native *AgentNativeService
}

func NewDeclarativeIntegrationRuntime(
	native *AgentNativeService,
) (*DeclarativeIntegrationRuntime, error) {
	if native == nil || native.db == nil {
		return nil, errors.New("agent native service is required")
	}
	return &DeclarativeIntegrationRuntime{native: native}, nil
}

type declarativeIntegrationDefinition struct {
	Version int                                    `json:"version"`
	Fields  map[string]declarativeIntegrationField `json:"fields"`
}

type declarativeIntegrationField struct {
	Pointer  string          `json:"pointer,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Type     string          `json:"type"`
	Required bool            `json:"required,omitempty"`
}

type integrationDestinationSpec struct {
	valueType string
	maxBytes  int
}

var ticketCreateIntegrationFields = map[string]integrationDestinationSpec{
	"title":            {valueType: "string", maxBytes: 2048},
	"description":      {valueType: "string", maxBytes: 64 << 10},
	"type":             {valueType: "string", maxBytes: 64},
	"priority":         {valueType: "string", maxBytes: 64},
	"tags":             {valueType: "array", maxBytes: 16 << 10},
	"customer_email":   {valueType: "string", maxBytes: 2048},
	"customer_phone":   {valueType: "string", maxBytes: 1024},
	"customer_name":    {valueType: "string", maxBytes: 2048},
	"custom_fields":    {valueType: "object", maxBytes: 64 << 10},
	"external_version": {valueType: "string", maxBytes: 1024},
}

var ticketCommentIntegrationFields = map[string]integrationDestinationSpec{
	"ticket_public_id": {valueType: "string", maxBytes: 64},
	"expected_version": {valueType: "integer", maxBytes: 32},
	"lease_id":         {valueType: "string", maxBytes: 1024},
	"content":          {valueType: "string", maxBytes: 64 << 10},
	"content_type":     {valueType: "string", maxBytes: 64},
	"comment_type":     {valueType: "string", maxBytes: 64},
	"reason":           {valueType: "string", maxBytes: 8 << 10},
	"external_version": {valueType: "string", maxBytes: 1024},
}

func (runtime *DeclarativeIntegrationRuntime) DryRun(
	_ context.Context,
	request IntegrationMappingDryRunRequest,
) (IntegrationMappingDryRunResult, error) {
	if request.Mapping == nil || request.Connection == nil ||
		request.Connector == nil {
		return IntegrationMappingDryRunResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	preview, warnings, err := applyDeclarativeIntegrationMapping(
		request.Mapping,
		request.Payload,
	)
	if err != nil {
		return IntegrationMappingDryRunResult{}, err
	}
	encoded, err := json.Marshal(preview)
	if err != nil || len(encoded) > maxIntegrationMappedBytes {
		return IntegrationMappingDryRunResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	return IntegrationMappingDryRunResult{
		MappingVersionID: request.Mapping.ID,
		TargetCommand:    request.Mapping.TargetCommand,
		Preview:          encoded,
		Warnings:         warnings,
	}, nil
}

func (runtime *DeclarativeIntegrationRuntime) Execute(
	ctx context.Context,
	tx *gorm.DB,
	command IntegrationDomainCommand,
) (IntegrationDomainCommandResult, error) {
	if runtime == nil || runtime.native == nil || tx == nil ||
		command.Connection == nil || command.Connector == nil ||
		command.Mapping == nil {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	if err := command.Operation.Validate(); err != nil ||
		command.Operation.Source != SourceProtocolConnector ||
		command.Operation.Actor != command.Connection.Actor() ||
		command.Operation.CredentialID != command.Connection.ActorCredentialID ||
		command.Operation.Scope.OrganizationID != command.Connection.OrganizationID ||
		command.Operation.Scope.ProjectID != command.Connection.ProjectID ||
		command.Connection.Status != models.ConnectionStatusActive ||
		command.Mapping.OrganizationID != command.Operation.Scope.OrganizationID ||
		command.Mapping.ProjectID != command.Operation.Scope.ProjectID ||
		command.Mapping.ConnectionID != command.Connection.ID ||
		(command.Mapping.Status != models.MappingVersionStatusPublished &&
			command.Mapping.Status != models.MappingVersionStatusRetired) ||
		command.Connector.OrganizationID != command.Operation.Scope.OrganizationID ||
		command.Connector.ProjectID != command.Operation.Scope.ProjectID ||
		command.Connector.ID != command.Connection.ConnectorDefinitionID ||
		command.Connector.Status != models.ConnectorDefinitionStatusActive ||
		(command.Connector.Direction != models.ConnectorDirectionInbound &&
			command.Connector.Direction != models.ConnectorDirectionBidirectional) {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeScopeMismatch
	}
	payloadDigest := sha256.Sum256(command.Payload)
	if hex.EncodeToString(payloadDigest[:]) != command.PayloadDigest {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	definitionDigest := sha256.Sum256(command.Mapping.Definition)
	if hex.EncodeToString(definitionDigest[:]) != command.Mapping.DefinitionDigest {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	mapped, _, err := applyDeclarativeIntegrationMapping(
		command.Mapping,
		command.Payload,
	)
	if err != nil {
		return IntegrationDomainCommandResult{}, err
	}
	if err := runtime.authorizeConnectionPrincipal(tx, command); err != nil {
		return IntegrationDomainCommandResult{}, err
	}
	commandContext, err := WithOperationContext(ctx, command.Operation)
	if err != nil {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeScopeMismatch
	}
	transactionContext := context.WithValue(
		commandContext,
		agentNativeTransactionContextKey{},
		tx,
	)
	var result IntegrationDomainCommandResult
	err = runtime.native.InTransaction(
		transactionContext,
		func(txContext context.Context, boundTx *gorm.DB) error {
			var executeErr error
			switch command.Mapping.TargetCommand {
			case "ticket.create":
				result, executeErr = runtime.executeTicketCreate(
					txContext,
					runtime.native,
					command,
					mapped,
				)
			case "ticket.comment.create":
				result, executeErr = runtime.executeTicketComment(
					txContext,
					boundTx,
					runtime.native,
					command,
					mapped,
				)
			default:
				executeErr = ErrIntegrationRuntimeUnsupportedCommand
			}
			return executeErr
		},
	)
	if err != nil {
		return IntegrationDomainCommandResult{}, err
	}
	return result, nil
}

func (runtime *DeclarativeIntegrationRuntime) authorizeConnectionPrincipal(
	tx *gorm.DB,
	command IntegrationDomainCommand,
) error {
	switch command.Operation.Actor.Type {
	case models.ActorTypeSystem:
		return nil
	case models.ActorTypeServicePrincipal:
	default:
		return ErrIntegrationRuntimeScopeMismatch
	}
	requiredScope := ""
	switch command.Mapping.TargetCommand {
	case "ticket.create":
		requiredScope = models.ScopeTicketsCreate
	case "ticket.comment.create":
		requiredScope = models.ScopeCommentsWrite
	default:
		return ErrIntegrationRuntimeUnsupportedCommand
	}
	var grant models.ProjectPrincipalGrant
	if err := tx.Where(
		"project_id = ? AND service_principal_id = ? AND is_active = ? AND (expires_at IS NULL OR expires_at > ?)",
		command.Operation.Scope.ProjectID,
		command.Operation.Actor.ID,
		true,
		runtime.native.now().UTC(),
	).First(&grant).Error; err != nil {
		return ErrIntegrationRuntimeScopeMismatch
	}
	if !grant.Role.IsValid() || !grant.HasScope(requiredScope) {
		return ErrIntegrationRuntimeScopeMismatch
	}
	return nil
}

func (runtime *DeclarativeIntegrationRuntime) executeTicketCreate(
	ctx context.Context,
	native *AgentNativeService,
	command IntegrationDomainCommand,
	mapped map[string]any,
) (IntegrationDomainCommandResult, error) {
	title, err := requiredIntegrationString(mapped, "title")
	if err != nil || utf8.RuneCountInString(title) > 255 {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	description, err := requiredIntegrationString(mapped, "description")
	if err != nil || utf8.RuneCountInString(description) > 10000 {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	ticketType := models.TicketTypeRequest
	if value, ok := optionalIntegrationString(mapped, "type"); ok {
		ticketType = models.TicketType(value)
	}
	if !ticketType.IsValid() {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	priority := models.TicketPriorityNormal
	if value, ok := optionalIntegrationString(mapped, "priority"); ok {
		priority = models.TicketPriority(value)
	}
	if !priority.IsValid() {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	tags, err := integrationStringList(mapped["tags"])
	if err != nil {
		return IntegrationDomainCommandResult{}, err
	}
	var customFields *models.JSONMap
	if value, exists := mapped["custom_fields"]; exists {
		object, ok := value.(map[string]any)
		if !ok || validateIntegrationCustomFields(object, 0) != nil {
			return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
		}
		converted := models.JSONMap(object)
		customFields = &converted
	}
	source := models.TicketSourceAPI
	if command.Operation.Actor.Type == models.ActorTypeServicePrincipal {
		source = models.TicketSourceAgent
	}
	result, err := native.CreateNativeTicket(ctx, NativeTicketCreateInput{
		Request: models.TicketCreateRequest{
			Title:         title,
			Description:   description,
			Type:          ticketType,
			Priority:      priority,
			Source:        source,
			Tags:          models.StringList(tags),
			CustomerEmail: integrationStringOrEmpty(mapped, "customer_email"),
			CustomerPhone: integrationStringOrEmpty(mapped, "customer_phone"),
			CustomerName:  integrationStringOrEmpty(mapped, "customer_name"),
			CustomFields:  customFields,
		},
		Actor:          command.Operation.Actor,
		CredentialID:   command.Operation.CredentialID,
		SourceProtocol: string(SourceProtocolConnector),
		RequestDigest:  command.PayloadDigest,
		TrustLevel:     models.TicketTrustLevelUntrusted,
		TraceID:        command.Operation.TraceID,
		CorrelationID:  command.Operation.CorrelationID,
	})
	if err != nil {
		return IntegrationDomainCommandResult{}, err
	}
	receiptData, err := json.Marshal(map[string]any{
		"ticket_public_id": result.Ticket.PublicID,
		"ticket_number":    result.Ticket.TicketNumber,
		"status":           result.Ticket.Status,
	})
	if err != nil {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	return IntegrationDomainCommandResult{
		Status:          models.InboxReceiptStatusApplied,
		ResourceType:    "ticket",
		ResourceID:      result.Ticket.PublicID,
		ResourceVersion: result.Ticket.Version,
		EventID:         result.Event.ID,
		OperationID:     result.Receipt.OperationID,
		ExternalVersion: integrationStringOrEmpty(mapped, "external_version"),
		ReceiptData:     receiptData,
	}, nil
}

func (runtime *DeclarativeIntegrationRuntime) executeTicketComment(
	ctx context.Context,
	tx *gorm.DB,
	native *AgentNativeService,
	command IntegrationDomainCommand,
	mapped map[string]any,
) (IntegrationDomainCommandResult, error) {
	ticketPublicID, err := requiredIntegrationString(mapped, "ticket_public_id")
	if err != nil || !canonicalIntegrationUUID(ticketPublicID) {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	expectedVersion, err := requiredIntegrationUint64(mapped, "expected_version")
	if err != nil || expectedVersion == 0 {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	content, err := requiredIntegrationString(mapped, "content")
	if err != nil || utf8.RuneCountInString(content) > 10000 {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	var ticket models.Ticket
	if err := tx.WithContext(ctx).Where(
		"public_id = ? AND organization_id = ? AND project_id = ?",
		ticketPublicID,
		command.Operation.Scope.OrganizationID,
		command.Operation.Scope.ProjectID,
	).First(&ticket).Error; err != nil {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	contentType := "text"
	if value, ok := optionalIntegrationString(mapped, "content_type"); ok {
		contentType = value
	}
	commentType := models.CommentTypePublic
	if value, ok := optionalIntegrationString(mapped, "comment_type"); ok {
		commentType = models.CommentType(value)
	}
	result, err := native.CreateComment(ctx, NativeCommentInput{
		TicketID:        ticket.ID,
		ExpectedVersion: expectedVersion,
		LeaseID:         integrationStringOrEmpty(mapped, "lease_id"),
		Actor:           command.Operation.Actor,
		CredentialID:    command.Operation.CredentialID,
		SourceProtocol:  string(SourceProtocolConnector),
		RequestDigest:   command.PayloadDigest,
		Content:         content,
		ContentType:     contentType,
		Type:            commentType,
		Reason:          integrationStringOrEmpty(mapped, "reason"),
		InputSources:    []string{"connector:" + command.Connection.PublicID},
		TraceID:         command.Operation.TraceID,
		CorrelationID:   command.Operation.CorrelationID,
	})
	if err != nil {
		return IntegrationDomainCommandResult{}, err
	}
	receiptData, err := json.Marshal(map[string]any{
		"comment_id":       result.Receipt.ResourceID,
		"ticket_public_id": ticket.PublicID,
	})
	if err != nil {
		return IntegrationDomainCommandResult{}, ErrIntegrationRuntimeInvalidMapping
	}
	return IntegrationDomainCommandResult{
		Status:          models.InboxReceiptStatusApplied,
		ResourceType:    "ticket_comment",
		ResourceID:      result.Receipt.ResourceID,
		ResourceVersion: result.Receipt.ResourceVersion,
		EventID:         result.Event.ID,
		OperationID:     result.Receipt.OperationID,
		ExternalVersion: integrationStringOrEmpty(mapped, "external_version"),
		ReceiptData:     receiptData,
	}, nil
}

func applyDeclarativeIntegrationMapping(
	mapping *models.MappingVersion,
	payload []byte,
) (map[string]any, []string, error) {
	if mapping == nil ||
		len(mapping.Definition) == 0 ||
		len(mapping.Definition) > maxIntegrationMappingDefinitionBytes ||
		len(payload) == 0 {
		return nil, nil, ErrIntegrationRuntimeInvalidMapping
	}
	specs, requiredDestinations, err := integrationTargetSpecs(
		mapping.TargetCommand,
	)
	if err != nil {
		return nil, nil, err
	}
	if err := validateIntegrationJSON(mapping.Definition, maxIntegrationJSONDepth); err != nil {
		return nil, nil, ErrIntegrationRuntimeInvalidMapping
	}
	var definition declarativeIntegrationDefinition
	if err := decodeStrictIntegrationJSON(mapping.Definition, &definition); err != nil ||
		definition.Version != 1 ||
		len(definition.Fields) == 0 ||
		len(definition.Fields) > maxIntegrationMappingFields {
		return nil, nil, ErrIntegrationRuntimeInvalidMapping
	}
	if err := validateIntegrationJSON(payload, maxIntegrationJSONDepth); err != nil {
		return nil, nil, ErrIntegrationRuntimeInvalidMapping
	}
	var document any
	if err := decodeIntegrationJSON(payload, &document); err != nil {
		return nil, nil, ErrIntegrationRuntimeInvalidMapping
	}
	if _, ok := document.(map[string]any); !ok {
		return nil, nil, ErrIntegrationRuntimeInvalidMapping
	}

	output := make(map[string]any, len(definition.Fields)+2)
	warnings := make([]string, 0)
	for destination, field := range definition.Fields {
		destination = strings.TrimSpace(destination)
		spec, allowed := specs[destination]
		if !allowed || field.Type != spec.valueType ||
			(field.Pointer == "") == (len(field.Value) == 0) {
			return nil, nil, ErrIntegrationRuntimeInvalidMapping
		}
		var value any
		found := true
		if field.Pointer != "" {
			if err := validateIntegrationJSONPointer(field.Pointer); err != nil {
				return nil, nil, ErrIntegrationRuntimeInvalidMapping
			}
			value, found, err = integrationJSONPointer(document, field.Pointer)
			if err != nil {
				return nil, nil, ErrIntegrationRuntimeInvalidMapping
			}
		} else {
			if err := validateIntegrationJSON(field.Value, maxIntegrationJSONDepth); err != nil ||
				decodeIntegrationJSON(field.Value, &value) != nil {
				return nil, nil, ErrIntegrationRuntimeInvalidMapping
			}
		}
		if !found || value == nil {
			if field.Required {
				return nil, nil, ErrIntegrationRuntimeInvalidMapping
			}
			warnings = append(warnings, "optional field "+destination+" is missing")
			continue
		}
		if err := validateIntegrationMappedValue(destination, value, spec); err != nil {
			return nil, nil, err
		}
		output[destination] = value
	}
	for _, destination := range requiredDestinations {
		if _, ok := output[destination]; !ok {
			return nil, nil, ErrIntegrationRuntimeInvalidMapping
		}
	}
	switch mapping.TargetCommand {
	case "ticket.create":
		if _, ok := output["type"]; !ok {
			output["type"] = string(models.TicketTypeRequest)
		}
		if _, ok := output["priority"]; !ok {
			output["priority"] = string(models.TicketPriorityNormal)
		}
	case "ticket.comment.create":
		if _, ok := output["content_type"]; !ok {
			output["content_type"] = "text"
		}
		if _, ok := output["comment_type"]; !ok {
			output["comment_type"] = string(models.CommentTypePublic)
		}
	}
	sort.Strings(warnings)
	encoded, err := json.Marshal(output)
	if err != nil || len(encoded) > maxIntegrationMappedBytes {
		return nil, nil, ErrIntegrationRuntimeInvalidMapping
	}
	return output, warnings, nil
}

func integrationTargetSpecs(
	command string,
) (map[string]integrationDestinationSpec, []string, error) {
	switch command {
	case "ticket.create":
		return ticketCreateIntegrationFields, []string{"title", "description"}, nil
	case "ticket.comment.create":
		return ticketCommentIntegrationFields,
			[]string{"ticket_public_id", "expected_version", "content"},
			nil
	default:
		return nil, nil, ErrIntegrationRuntimeUnsupportedCommand
	}
}

func validateIntegrationMappedValue(
	destination string,
	value any,
	spec integrationDestinationSpec,
) error {
	if integrationJSONValueType(value) != spec.valueType {
		return ErrIntegrationRuntimeInvalidMapping
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > spec.maxBytes {
		return ErrIntegrationRuntimeInvalidMapping
	}
	switch destination {
	case "title", "description", "content", "type", "priority",
		"customer_email", "customer_phone", "customer_name",
		"external_version", "ticket_public_id", "lease_id",
		"content_type", "comment_type", "reason":
		if err := validateIntegrationMappedString(
			destination,
			value.(string),
		); err != nil {
			return err
		}
		if destination == "ticket_public_id" &&
			!canonicalIntegrationUUID(value.(string)) {
			return ErrIntegrationRuntimeInvalidMapping
		}
	case "expected_version":
		parsed, err := integrationUint64(value)
		if err != nil || parsed == 0 {
			return ErrIntegrationRuntimeInvalidMapping
		}
	case "custom_fields":
		if validateIntegrationCustomFields(value.(map[string]any), 0) != nil {
			return ErrIntegrationRuntimeInvalidMapping
		}
	case "tags":
		if _, err := integrationStringList(value); err != nil {
			return err
		}
	}
	return nil
}

func integrationJSONValueType(value any) string {
	switch typed := value.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number:
		if strings.ContainsAny(typed.String(), ".eE") {
			return "number"
		}
		return "integer"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	default:
		return ""
	}
}

func validateIntegrationJSONPointer(pointer string) error {
	if pointer == "" || len(pointer) > 512 || pointer[0] != '/' ||
		strings.ContainsFunc(pointer, unicode.IsControl) {
		return ErrIntegrationRuntimeInvalidMapping
	}
	segments := strings.Split(pointer[1:], "/")
	if len(segments) == 0 || len(segments) > maxIntegrationJSONDepth {
		return ErrIntegrationRuntimeInvalidMapping
	}
	for _, segment := range segments {
		for index := 0; index < len(segment); index++ {
			if segment[index] != '~' {
				continue
			}
			if index+1 >= len(segment) ||
				(segment[index+1] != '0' && segment[index+1] != '1') {
				return ErrIntegrationRuntimeInvalidMapping
			}
			index++
		}
	}
	return nil
}

func integrationJSONPointer(document any, pointer string) (any, bool, error) {
	current := document
	for _, encoded := range strings.Split(pointer[1:], "/") {
		segment := strings.ReplaceAll(
			strings.ReplaceAll(encoded, "~1", "/"),
			"~0",
			"~",
		)
		switch typed := current.(type) {
		case map[string]any:
			value, ok := typed[segment]
			if !ok {
				return nil, false, nil
			}
			current = value
		case []any:
			if segment == "" ||
				(len(segment) > 1 && segment[0] == '0') ||
				strings.HasPrefix(segment, "+") ||
				strings.HasPrefix(segment, "-") {
				return nil, false, ErrIntegrationRuntimeInvalidMapping
			}
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false, nil
			}
			current = typed[index]
		default:
			return nil, false, nil
		}
	}
	return current, true, nil
}

func decodeStrictIntegrationJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON must contain one value")
	}
	return nil
}

func decodeIntegrationJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON must contain one value")
	}
	return nil
}

func validateIntegrationJSON(raw []byte, maximumDepth int) error {
	if !utf8.Valid(raw) {
		return errors.New("JSON must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if err := consumeIntegrationJSONToken(decoder, token, 0, maximumDepth); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("JSON must contain one value")
	}
	return nil
}

func validateIntegrationMappedString(destination, value string) error {
	maximumRunes := 0
	maximumBytes := 0
	requireNonEmpty := false
	switch destination {
	case "title":
		maximumRunes, maximumBytes, requireNonEmpty = 255, 1020, true
	case "description", "content":
		maximumRunes, maximumBytes, requireNonEmpty = 10000, 40000, true
	case "type", "priority", "content_type", "comment_type":
		maximumRunes, maximumBytes = 32, 64
	case "customer_email":
		maximumRunes, maximumBytes = 320, 1280
	case "customer_phone":
		maximumRunes, maximumBytes = 128, 512
	case "customer_name":
		maximumRunes, maximumBytes = 255, 1020
	case "external_version", "lease_id":
		maximumRunes, maximumBytes = 128, 512
	case "ticket_public_id":
		maximumRunes, maximumBytes, requireNonEmpty = 36, 36, true
	case "reason":
		maximumRunes, maximumBytes = 1000, 4000
	default:
		return ErrIntegrationRuntimeInvalidMapping
	}
	if requireNonEmpty && strings.TrimSpace(value) == "" {
		return ErrIntegrationRuntimeInvalidMapping
	}
	if !utf8.ValidString(value) ||
		utf8.RuneCountInString(value) > maximumRunes ||
		len(value) > maximumBytes {
		return ErrIntegrationRuntimeInvalidMapping
	}
	return nil
}

func consumeIntegrationJSONToken(
	decoder *json.Decoder,
	token json.Token,
	depth int,
	maximumDepth int,
) error {
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	if depth >= maximumDepth {
		return errors.New("JSON nesting exceeds limit")
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is invalid")
			}
			if _, exists := keys[key]; exists {
				return errors.New("duplicate JSON object key")
			}
			keys[key] = struct{}{}
			valueToken, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := consumeIntegrationJSONToken(
				decoder,
				valueToken,
				depth+1,
				maximumDepth,
			); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			valueToken, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := consumeIntegrationJSONToken(
				decoder,
				valueToken,
				depth+1,
				maximumDepth,
			); err != nil {
				return err
			}
		}
	default:
		return errors.New("unsupported JSON delimiter")
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	expected := json.Delim('}')
	if delimiter == '[' {
		expected = ']'
	}
	if closing != expected {
		return errors.New("mismatched JSON delimiter")
	}
	return nil
}

func requiredIntegrationString(
	values map[string]any,
	key string,
) (string, error) {
	value, ok := values[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", ErrIntegrationRuntimeInvalidMapping
	}
	return value, nil
}

func optionalIntegrationString(
	values map[string]any,
	key string,
) (string, bool) {
	value, ok := values[key].(string)
	return value, ok
}

func integrationStringOrEmpty(values map[string]any, key string) string {
	value, _ := optionalIntegrationString(values, key)
	return value
}

func requiredIntegrationUint64(
	values map[string]any,
	key string,
) (uint64, error) {
	value, ok := values[key]
	if !ok {
		return 0, ErrIntegrationRuntimeInvalidMapping
	}
	return integrationUint64(value)
}

func integrationUint64(value any) (uint64, error) {
	number, ok := value.(json.Number)
	if !ok || strings.ContainsAny(number.String(), ".eE") {
		return 0, ErrIntegrationRuntimeInvalidMapping
	}
	parsed, err := strconv.ParseUint(number.String(), 10, 64)
	if err != nil {
		return 0, ErrIntegrationRuntimeInvalidMapping
	}
	return parsed, nil
}

func integrationStringList(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok || len(items) > 64 {
		return nil, ErrIntegrationRuntimeInvalidMapping
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" || len(text) > 128 {
			return nil, ErrIntegrationRuntimeInvalidMapping
		}
		result = append(result, text)
	}
	return result, nil
}

var forbiddenIntegrationCustomFieldKeys = map[string]struct{}{
	"actor":                {},
	"actor_id":             {},
	"actor_type":           {},
	"business_unit_id":     {},
	"credential":           {},
	"credential_id":        {},
	"external_id":          {},
	"external_resource_id": {},
	"grant":                {},
	"grants":               {},
	"organization":         {},
	"organization_id":      {},
	"permission":           {},
	"permissions":          {},
	"principal":            {},
	"project":              {},
	"project_id":           {},
	"project_key":          {},
	"queue":                {},
	"queue_id":             {},
	"queue_key":            {},
	"role":                 {},
	"roles":                {},
}

var forbiddenCompactIntegrationCustomFieldKeys = map[string]struct{}{
	"actor":              {},
	"actorid":            {},
	"actortype":          {},
	"businessunitid":     {},
	"credential":         {},
	"credentialid":       {},
	"externalid":         {},
	"externalresourceid": {},
	"grant":              {},
	"grants":             {},
	"organization":       {},
	"organizationid":     {},
	"permission":         {},
	"permissions":        {},
	"principal":          {},
	"project":            {},
	"projectid":          {},
	"projectkey":         {},
	"queue":              {},
	"queueid":            {},
	"queuekey":           {},
	"role":               {},
	"roles":              {},
}

func validateIntegrationCustomFields(value any, depth int) error {
	if depth > 10 {
		return ErrIntegrationRuntimeInvalidMapping
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > 128 {
			return ErrIntegrationRuntimeInvalidMapping
		}
		for key, child := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			normalized = strings.ReplaceAll(normalized, "-", "_")
			if normalized == "" || len(key) > 128 ||
				strings.ContainsFunc(key, unicode.IsControl) {
				return ErrIntegrationRuntimeInvalidMapping
			}
			if _, forbidden := forbiddenIntegrationCustomFieldKeys[normalized]; forbidden {
				return ErrIntegrationRuntimeInvalidMapping
			}
			compact := strings.Map(func(character rune) rune {
				if unicode.IsLetter(character) || unicode.IsDigit(character) {
					return unicode.ToLower(character)
				}
				return -1
			}, normalized)
			if _, forbidden := forbiddenCompactIntegrationCustomFieldKeys[compact]; forbidden {
				return ErrIntegrationRuntimeInvalidMapping
			}
			if err := validateIntegrationCustomFields(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if len(typed) > 256 {
			return ErrIntegrationRuntimeInvalidMapping
		}
		for _, child := range typed {
			if err := validateIntegrationCustomFields(child, depth+1); err != nil {
				return err
			}
		}
	case string:
		if len(typed) > 16<<10 {
			return ErrIntegrationRuntimeInvalidMapping
		}
	case json.Number:
		if _, err := strconv.ParseFloat(typed.String(), 64); err != nil {
			return ErrIntegrationRuntimeInvalidMapping
		}
	case bool, nil:
	default:
		return ErrIntegrationRuntimeInvalidMapping
	}
	return nil
}

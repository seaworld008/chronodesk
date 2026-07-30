package services

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

const (
	ReferenceSolutionSchemaVersion         = "chronodesk.reference-solution.v1"
	ReferenceSolutionVersion               = "1.0.0"
	ReferenceSolutionBlueprintExtensionKey = "reference_blueprint"

	ReferenceSolutionITSREPackageKey              = "reference.it-sre-service-desk"
	ReferenceSolutionHRAdminPackageKey            = "reference.hr-admin-shared-services"
	ReferenceSolutionFinanceProcurementPackageKey = "reference.finance-procurement"

	ReferenceSolutionITSREID              = "019fb0a0-0000-7000-8000-000000000001"
	ReferenceSolutionHRAdminID            = "019fb0a0-0000-7000-8000-000000000002"
	ReferenceSolutionFinanceProcurementID = "019fb0a0-0000-7000-8000-000000000003"
)

var (
	ErrReferenceSolutionInvalid = errors.New("reference solution package is invalid")
	referenceTemplateKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	referenceEventTypePattern   = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
)

type ReferenceAgentAction string

const (
	ReferenceAgentClassify          ReferenceAgentAction = "classify"
	ReferenceAgentSummarize         ReferenceAgentAction = "summarize"
	ReferenceAgentSuggestRoute      ReferenceAgentAction = "suggest_route"
	ReferenceAgentDraftReply        ReferenceAgentAction = "draft_reply"
	ReferenceAgentRetrieveKnowledge ReferenceAgentAction = "retrieve_knowledge"
	ReferenceAgentSuggestResolution ReferenceAgentAction = "suggest_resolution"
)

func (action ReferenceAgentAction) IsValid() bool {
	switch action {
	case ReferenceAgentClassify,
		ReferenceAgentSummarize,
		ReferenceAgentSuggestRoute,
		ReferenceAgentDraftReply,
		ReferenceAgentRetrieveKnowledge,
		ReferenceAgentSuggestResolution:
		return true
	default:
		return false
	}
}

type ReferenceAgentTemplate struct {
	Key                     string                 `json:"key"`
	Name                    string                 `json:"name"`
	Purpose                 string                 `json:"purpose"`
	ProjectRole             models.ProjectRole     `json:"project_role"`
	AllowedActions          []ReferenceAgentAction `json:"allowed_actions"`
	KnowledgeCollectionKeys []string               `json:"knowledge_collection_keys"`
	RequiresHumanApproval   bool                   `json:"requires_human_approval"`
}

func (template ReferenceAgentTemplate) Validate() error {
	if err := validateReferenceTemplateKey(template.Key, "agent"); err != nil {
		return err
	}
	if strings.TrimSpace(template.Name) == "" ||
		strings.TrimSpace(template.Purpose) == "" ||
		len(template.Purpose) > 500 ||
		!template.ProjectRole.IsValid() {
		return fmt.Errorf("reference agent template %q is invalid", template.Key)
	}
	if len(template.AllowedActions) == 0 ||
		len(template.AllowedActions) > 16 {
		return fmt.Errorf(
			"reference agent template %q has invalid action count",
			template.Key,
		)
	}
	actions := make(map[ReferenceAgentAction]struct{})
	for _, action := range template.AllowedActions {
		if !action.IsValid() {
			return fmt.Errorf(
				"reference agent template %q has invalid action %q",
				template.Key,
				action,
			)
		}
		if _, duplicate := actions[action]; duplicate {
			return fmt.Errorf(
				"reference agent template %q repeats action %q",
				template.Key,
				action,
			)
		}
		actions[action] = struct{}{}
	}
	if err := validateReferenceKeyList(
		template.KnowledgeCollectionKeys,
		"agent knowledge collection",
	); err != nil {
		return err
	}
	return nil
}

type ReferenceMetricMeasure string

const (
	ReferenceMetricCount           ReferenceMetricMeasure = "count"
	ReferenceMetricRatio           ReferenceMetricMeasure = "ratio"
	ReferenceMetricDurationMinutes ReferenceMetricMeasure = "duration_minutes"
)

func (measure ReferenceMetricMeasure) IsValid() bool {
	switch measure {
	case ReferenceMetricCount,
		ReferenceMetricRatio,
		ReferenceMetricDurationMinutes:
		return true
	default:
		return false
	}
}

type ReferenceMetricTemplate struct {
	Key              string                 `json:"key"`
	Name             string                 `json:"name"`
	Measure          ReferenceMetricMeasure `json:"measure"`
	SourceEvent      string                 `json:"source_event,omitempty"`
	DenominatorEvent string                 `json:"denominator_event,omitempty"`
	StartEvent       string                 `json:"start_event,omitempty"`
	EndEvent         string                 `json:"end_event,omitempty"`
	Unit             string                 `json:"unit"`
	Dimensions       []string               `json:"dimensions"`
}

func (template ReferenceMetricTemplate) Validate() error {
	if err := validateReferenceTemplateKey(template.Key, "metric"); err != nil {
		return err
	}
	if strings.TrimSpace(template.Name) == "" ||
		!template.Measure.IsValid() {
		return fmt.Errorf("reference metric template %q is invalid", template.Key)
	}
	switch template.Measure {
	case ReferenceMetricCount:
		if !referenceEventTypePattern.MatchString(template.SourceEvent) ||
			template.DenominatorEvent != "" ||
			template.StartEvent != "" ||
			template.EndEvent != "" {
			return fmt.Errorf(
				"reference count metric %q has invalid events",
				template.Key,
			)
		}
	case ReferenceMetricRatio:
		if !referenceEventTypePattern.MatchString(template.SourceEvent) ||
			!referenceEventTypePattern.MatchString(
				template.DenominatorEvent,
			) ||
			template.StartEvent != "" ||
			template.EndEvent != "" {
			return fmt.Errorf(
				"reference ratio metric %q has invalid events",
				template.Key,
			)
		}
	case ReferenceMetricDurationMinutes:
		if !referenceEventTypePattern.MatchString(template.StartEvent) ||
			!referenceEventTypePattern.MatchString(template.EndEvent) ||
			template.SourceEvent != "" ||
			template.DenominatorEvent != "" {
			return fmt.Errorf(
				"reference duration metric %q has invalid events",
				template.Key,
			)
		}
	}
	expectedUnit := map[ReferenceMetricMeasure]string{
		ReferenceMetricCount:           "count",
		ReferenceMetricRatio:           "percent",
		ReferenceMetricDurationMinutes: "minutes",
	}[template.Measure]
	if template.Unit != expectedUnit {
		return fmt.Errorf(
			"reference metric template %q has invalid unit %q",
			template.Key,
			template.Unit,
		)
	}
	allowedDimensions := map[string]struct{}{
		"lifecycle_category": {},
		"work_class":         {},
		"priority":           {},
		"queue":              {},
		"request_type":       {},
	}
	if len(template.Dimensions) == 0 ||
		len(template.Dimensions) > len(allowedDimensions) {
		return fmt.Errorf(
			"reference metric template %q has invalid dimensions",
			template.Key,
		)
	}
	seen := make(map[string]struct{}, len(template.Dimensions))
	for _, dimension := range template.Dimensions {
		if _, allowed := allowedDimensions[dimension]; !allowed {
			return fmt.Errorf(
				"reference metric template %q has invalid dimension %q",
				template.Key,
				dimension,
			)
		}
		if _, duplicate := seen[dimension]; duplicate {
			return fmt.Errorf(
				"reference metric template %q repeats dimension %q",
				template.Key,
				dimension,
			)
		}
		seen[dimension] = struct{}{}
	}
	return nil
}

type ReferenceKnowledgeTemplate struct {
	Key               string               `json:"key"`
	Name              string               `json:"name"`
	CollectionKey     string               `json:"collection_key"`
	SeedArticleTitles []string             `json:"seed_article_titles"`
	ReaderRoles       []models.ProjectRole `json:"reader_roles"`
}

func (template ReferenceKnowledgeTemplate) Validate() error {
	if err := validateReferenceTemplateKey(template.Key, "knowledge"); err != nil {
		return err
	}
	if strings.TrimSpace(template.Name) == "" {
		return fmt.Errorf(
			"reference knowledge template %q requires a name",
			template.Key,
		)
	}
	if err := validateReferenceTemplateKey(
		template.CollectionKey,
		"knowledge collection",
	); err != nil {
		return err
	}
	if len(template.SeedArticleTitles) == 0 ||
		len(template.SeedArticleTitles) > 32 {
		return fmt.Errorf(
			"reference knowledge template %q has invalid seed articles",
			template.Key,
		)
	}
	for _, title := range template.SeedArticleTitles {
		if strings.TrimSpace(title) == "" ||
			len(title) > 240 ||
			strings.ContainsAny(title, "\r\n") {
			return fmt.Errorf(
				"reference knowledge template %q has an invalid seed title",
				template.Key,
			)
		}
	}
	if len(template.ReaderRoles) == 0 {
		return fmt.Errorf(
			"reference knowledge template %q requires reader roles",
			template.Key,
		)
	}
	for _, role := range template.ReaderRoles {
		if !role.IsValid() {
			return fmt.Errorf(
				"reference knowledge template %q has invalid role %q",
				template.Key,
				role,
			)
		}
	}
	return nil
}

type ReferenceConnectorKind string

const (
	ReferenceConnectorMonitoring  ReferenceConnectorKind = "monitoring"
	ReferenceConnectorChat        ReferenceConnectorKind = "chat"
	ReferenceConnectorHRIS        ReferenceConnectorKind = "hris"
	ReferenceConnectorERP         ReferenceConnectorKind = "erp"
	ReferenceConnectorProcurement ReferenceConnectorKind = "procurement"
	ReferenceConnectorEmail       ReferenceConnectorKind = "email"
)

func (kind ReferenceConnectorKind) IsValid() bool {
	switch kind {
	case ReferenceConnectorMonitoring,
		ReferenceConnectorChat,
		ReferenceConnectorHRIS,
		ReferenceConnectorERP,
		ReferenceConnectorProcurement,
		ReferenceConnectorEmail:
		return true
	default:
		return false
	}
}

type ReferenceConnectorDirection string

const (
	ReferenceConnectorInbound       ReferenceConnectorDirection = "inbound"
	ReferenceConnectorOutbound      ReferenceConnectorDirection = "outbound"
	ReferenceConnectorBidirectional ReferenceConnectorDirection = "bidirectional"
)

func (direction ReferenceConnectorDirection) IsValid() bool {
	switch direction {
	case ReferenceConnectorInbound,
		ReferenceConnectorOutbound,
		ReferenceConnectorBidirectional:
		return true
	default:
		return false
	}
}

type ReferenceConnectorTemplate struct {
	Key            string                      `json:"key"`
	Name           string                      `json:"name"`
	Kind           ReferenceConnectorKind      `json:"kind"`
	Direction      ReferenceConnectorDirection `json:"direction"`
	EventTypes     []string                    `json:"event_types"`
	RequiredScopes []string                    `json:"required_scopes"`
}

func (template ReferenceConnectorTemplate) Validate() error {
	if err := validateReferenceTemplateKey(template.Key, "connector"); err != nil {
		return err
	}
	if strings.TrimSpace(template.Name) == "" ||
		!template.Kind.IsValid() ||
		!template.Direction.IsValid() {
		return fmt.Errorf(
			"reference connector template %q is invalid",
			template.Key,
		)
	}
	if len(template.EventTypes) == 0 || len(template.EventTypes) > 32 {
		return fmt.Errorf(
			"reference connector template %q has invalid events",
			template.Key,
		)
	}
	for _, eventType := range template.EventTypes {
		if !referenceEventTypePattern.MatchString(eventType) {
			return fmt.Errorf(
				"reference connector template %q has invalid event %q",
				template.Key,
				eventType,
			)
		}
	}
	allowedScopes := map[string]struct{}{
		models.ScopeTicketsRead:     {},
		models.ScopeTicketsCreate:   {},
		models.ScopeTicketsUpdate:   {},
		models.ScopeCommentsWrite:   {},
		models.ScopeEventsSubscribe: {},
	}
	if len(template.RequiredScopes) == 0 ||
		len(template.RequiredScopes) > len(allowedScopes) {
		return fmt.Errorf(
			"reference connector template %q has invalid scopes",
			template.Key,
		)
	}
	for _, scope := range template.RequiredScopes {
		if _, allowed := allowedScopes[scope]; !allowed {
			return fmt.Errorf(
				"reference connector template %q has invalid scope %q",
				template.Key,
				scope,
			)
		}
	}
	return nil
}

type ReferenceSolutionBlueprint struct {
	AgentTemplates     []ReferenceAgentTemplate     `json:"agent_templates"`
	MetricTemplates    []ReferenceMetricTemplate    `json:"metric_templates"`
	KnowledgeTemplates []ReferenceKnowledgeTemplate `json:"knowledge_templates"`
	ConnectorTemplates []ReferenceConnectorTemplate `json:"connector_templates"`
}

func (blueprint ReferenceSolutionBlueprint) Validate() error {
	if len(blueprint.AgentTemplates) == 0 ||
		len(blueprint.MetricTemplates) == 0 ||
		len(blueprint.KnowledgeTemplates) == 0 ||
		len(blueprint.ConnectorTemplates) == 0 {
		return errors.New(
			"reference blueprint requires agent, metric, knowledge and connector templates",
		)
	}
	if err := validateReferenceTemplates(
		blueprint.AgentTemplates,
		func(template ReferenceAgentTemplate) string { return template.Key },
		func(template ReferenceAgentTemplate) error { return template.Validate() },
	); err != nil {
		return err
	}
	if err := validateReferenceTemplates(
		blueprint.MetricTemplates,
		func(template ReferenceMetricTemplate) string { return template.Key },
		func(template ReferenceMetricTemplate) error { return template.Validate() },
	); err != nil {
		return err
	}
	if err := validateReferenceTemplates(
		blueprint.KnowledgeTemplates,
		func(template ReferenceKnowledgeTemplate) string { return template.Key },
		func(template ReferenceKnowledgeTemplate) error { return template.Validate() },
	); err != nil {
		return err
	}
	return validateReferenceTemplates(
		blueprint.ConnectorTemplates,
		func(template ReferenceConnectorTemplate) string { return template.Key },
		func(template ReferenceConnectorTemplate) error { return template.Validate() },
	)
}

type ReferenceSolutionPackage struct {
	SchemaVersion      string                         `json:"schema_version"`
	ID                 string                         `json:"id"`
	Core               models.IndustrySolutionPackage `json:"core"`
	Blueprint          ReferenceSolutionBlueprint     `json:"blueprint"`
	BlueprintDigest    string                         `json:"blueprint_digest"`
	SignatureAlgorithm string                         `json:"signature_algorithm"`
	SignerKeyID        string                         `json:"signer_key_id"`
	Signature          []byte                         `json:"signature"`
}

func (bundle ReferenceSolutionPackage) Verify(
	publicKey ed25519.PublicKey,
) error {
	if bundle.SchemaVersion != ReferenceSolutionSchemaVersion {
		return fmt.Errorf(
			"%w: schema version %q",
			ErrReferenceSolutionInvalid,
			bundle.SchemaVersion,
		)
	}
	if _, err := uuid.Parse(bundle.ID); err != nil {
		return fmt.Errorf("%w: bundle id", ErrReferenceSolutionInvalid)
	}
	if err := bundle.Core.Verify(publicKey); err != nil {
		return fmt.Errorf("%w: %v", ErrReferenceSolutionInvalid, err)
	}
	if err := bundle.Blueprint.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrReferenceSolutionInvalid, err)
	}
	computedDigest, err := referenceCanonicalDigest(bundle.Blueprint)
	if err != nil {
		return err
	}
	embeddedBlueprint, err := referenceBlueprintFromSnapshot(
		bundle.Core.Snapshot,
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrReferenceSolutionInvalid, err)
	}
	embeddedDigest, err := referenceCanonicalDigest(embeddedBlueprint)
	if err != nil {
		return err
	}
	if computedDigest != bundle.BlueprintDigest ||
		embeddedDigest != bundle.BlueprintDigest ||
		bundle.SignatureAlgorithm != "ed25519" ||
		bundle.SignerKeyID != bundle.Core.SignerKeyID ||
		len(publicKey) != ed25519.PublicKeySize ||
		len(bundle.Signature) != ed25519.SignatureSize {
		return ErrReferenceSolutionInvalid
	}
	payload, err := bundle.signaturePayload()
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, payload, bundle.Signature) {
		return ErrReferenceSolutionInvalid
	}
	return nil
}

func (bundle ReferenceSolutionPackage) Export() ([]byte, error) {
	if bundle.SchemaVersion != ReferenceSolutionSchemaVersion ||
		len(bundle.Signature) != ed25519.SignatureSize {
		return nil, ErrReferenceSolutionInvalid
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("export reference solution package: %w", err)
	}
	return encoded, nil
}

func ParseReferenceSolutionPackage(
	raw []byte,
) (*ReferenceSolutionPackage, error) {
	var bundle ReferenceSolutionPackage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return nil, fmt.Errorf("parse reference solution package: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New(
				"parse reference solution package: multiple JSON values",
			)
		}
		return nil, fmt.Errorf("parse reference solution package: %w", err)
	}
	return &bundle, nil
}

func (bundle ReferenceSolutionPackage) signaturePayload() ([]byte, error) {
	payload := struct {
		SchemaVersion   string `json:"schema_version"`
		ID              string `json:"id"`
		PackageKey      string `json:"package_key"`
		PackageVersion  string `json:"package_version"`
		CoreContentHash string `json:"core_content_hash"`
		BlueprintDigest string `json:"blueprint_digest"`
		SignerKeyID     string `json:"signer_key_id"`
	}{
		SchemaVersion:   bundle.SchemaVersion,
		ID:              bundle.ID,
		PackageKey:      bundle.Core.Manifest.PackageKey,
		PackageVersion:  bundle.Core.Manifest.Version,
		CoreContentHash: bundle.Core.Manifest.ContentHash,
		BlueprintDigest: bundle.BlueprintDigest,
		SignerKeyID:     bundle.SignerKeyID,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode reference solution signature: %w", err)
	}
	return encoded, nil
}

func BuildReferenceSolutionPackages(
	signerKeyID string,
	privateKey ed25519.PrivateKey,
) ([]ReferenceSolutionPackage, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("reference solution signing key is invalid")
	}
	signerKeyID = strings.TrimSpace(signerKeyID)
	if signerKeyID == "" || len(signerKeyID) > 128 {
		return nil, errors.New("reference solution signer key id is invalid")
	}
	specifications := referenceSolutionSpecifications()
	result := make([]ReferenceSolutionPackage, 0, len(specifications))
	for _, specification := range specifications {
		if err := specification.Blueprint.Validate(); err != nil {
			return nil, err
		}
		coreSnapshot, err := buildReferenceCoreSnapshot(specification)
		if err != nil {
			return nil, fmt.Errorf(
				"build reference solution %q: %w",
				specification.PackageKey,
				err,
			)
		}
		core, err := models.SignIndustrySolutionPackage(
			models.IndustrySolutionManifest{
				SchemaVersion:      "1.0",
				PackageKey:         specification.PackageKey,
				Name:               specification.Name,
				Industry:           specification.Industry,
				Version:            ReferenceSolutionVersion,
				Terminology:        specification.Terminology,
				TemplateReferences: referenceCoreTemplateReferences(coreSnapshot),
			},
			coreSnapshot,
			signerKeyID,
			privateKey,
		)
		if err != nil {
			return nil, err
		}
		blueprintDigest, err := referenceCanonicalDigest(
			specification.Blueprint,
		)
		if err != nil {
			return nil, err
		}
		bundle := ReferenceSolutionPackage{
			SchemaVersion:      ReferenceSolutionSchemaVersion,
			ID:                 specification.ID,
			Core:               *core,
			Blueprint:          specification.Blueprint,
			BlueprintDigest:    blueprintDigest,
			SignatureAlgorithm: "ed25519",
			SignerKeyID:        signerKeyID,
		}
		payload, err := bundle.signaturePayload()
		if err != nil {
			return nil, err
		}
		bundle.Signature = ed25519.Sign(privateKey, payload)
		result = append(result, bundle)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Core.Manifest.PackageKey <
			result[j].Core.Manifest.PackageKey
	})
	return result, nil
}

func FindReferenceSolutionPackage(
	packages []ReferenceSolutionPackage,
	packageKey string,
) (*ReferenceSolutionPackage, bool) {
	packageKey = strings.TrimSpace(packageKey)
	for index := range packages {
		if packages[index].Core.Manifest.PackageKey == packageKey {
			return &packages[index], true
		}
	}
	return nil, false
}

func (service *ProjectConfigurationService) PreviewReferenceSolutionUpgrade(
	ctx context.Context,
	bundle ReferenceSolutionPackage,
	publicKey ed25519.PublicKey,
) (*SolutionUpgradePreview, error) {
	if err := bundle.Verify(publicKey); err != nil {
		return nil, err
	}
	return service.PreviewSolutionUpgrade(ctx, bundle.Core, publicKey)
}

func (service *ProjectConfigurationService) PrepareReferenceSolutionInstallation(
	ctx context.Context,
	bundle ReferenceSolutionPackage,
	publicKey ed25519.PublicKey,
) (*models.ProjectSolutionInstallation, error) {
	if err := bundle.Verify(publicKey); err != nil {
		return nil, err
	}
	return service.PrepareSolutionInstallation(ctx, bundle.Core, publicKey)
}

type referenceSolutionSpecification struct {
	ID                string
	PackageKey        string
	Name              string
	Industry          string
	Prefix            string
	Terminology       map[string]string
	RequestTypes      []referenceRequestTypeSpecification
	ResponseMinutes   uint
	ResolutionMinutes uint
	RiskWorkClass     models.WorkClass
	Blueprint         ReferenceSolutionBlueprint
}

type referenceRequestTypeSpecification struct {
	Key         string
	Name        string
	Description string
	WorkClass   models.WorkClass
	ExtraFields []referenceSchemaField
}

type referenceSchemaField struct {
	Key      string
	Title    string
	Type     string
	Format   string
	Required bool
	Enum     []string
}

func referenceSolutionSpecifications() []referenceSolutionSpecification {
	return []referenceSolutionSpecification{
		itSREReferenceSpecification(),
		hrAdminReferenceSpecification(),
		financeProcurementReferenceSpecification(),
	}
}

func itSREReferenceSpecification() referenceSolutionSpecification {
	return referenceSolutionSpecification{
		ID:         ReferenceSolutionITSREID,
		PackageKey: ReferenceSolutionITSREPackageKey,
		Name:       "IT/SRE 服务台",
		Industry:   "technology",
		Prefix:     "it",
		Terminology: map[string]string{
			"ticket":     "工单",
			"requester":  "报障人",
			"agent":      "值班工程师",
			"queue":      "服务队列",
			"incident":   "事件",
			"service":    "服务请求",
			"change":     "变更",
			"resolution": "恢复方案",
			"approval":   "变更审批",
		},
		RequestTypes: []referenceRequestTypeSpecification{
			{
				Key:         "it_incident",
				Name:        "生产事件",
				Description: "服务中断、性能下降与告警事件",
				WorkClass:   models.WorkClassIncident,
				ExtraFields: []referenceSchemaField{
					{
						Key: "service_name", Title: "服务名称",
						Type: "string", Required: true,
					},
					{
						Key: "environment", Title: "环境",
						Type: "string", Required: true,
						Enum: []string{"production", "staging", "development"},
					},
					{
						Key: "impact_scope", Title: "影响范围",
						Type: "string", Required: true,
					},
				},
			},
			{
				Key:         "it_service_request",
				Name:        "IT 服务请求",
				Description: "账号、权限、软件与基础设施服务请求",
				WorkClass:   models.WorkClassRequest,
				ExtraFields: []referenceSchemaField{
					{
						Key: "service_catalog_item", Title: "服务目录项",
						Type: "string", Required: true,
					},
					{
						Key: "needed_by", Title: "期望完成日期",
						Type: "string", Format: "date",
					},
				},
			},
			{
				Key:         "it_change",
				Name:        "生产变更",
				Description: "需要评估、审批和回退方案的生产变更",
				WorkClass:   models.WorkClassChange,
				ExtraFields: []referenceSchemaField{
					{
						Key: "change_window", Title: "变更窗口",
						Type: "string", Format: "date-time", Required: true,
					},
					{
						Key: "rollback_plan", Title: "回退方案",
						Type: "string", Required: true,
					},
				},
			},
		},
		ResponseMinutes:   15,
		ResolutionMinutes: 240,
		RiskWorkClass:     models.WorkClassChange,
		Blueprint: ReferenceSolutionBlueprint{
			AgentTemplates: []ReferenceAgentTemplate{
				{
					Key: "it_triage_agent", Name: "事件分诊 Agent",
					Purpose:     "分类生产事件、建议路由并检索标准恢复手册",
					ProjectRole: models.ProjectRoleAgent,
					AllowedActions: []ReferenceAgentAction{
						ReferenceAgentClassify,
						ReferenceAgentSummarize,
						ReferenceAgentSuggestRoute,
						ReferenceAgentRetrieveKnowledge,
						ReferenceAgentSuggestResolution,
					},
					KnowledgeCollectionKeys: []string{"it_runbooks"},
					RequiresHumanApproval:   true,
				},
			},
			MetricTemplates: []ReferenceMetricTemplate{
				{
					Key: "it_mttr", Name: "平均恢复时间",
					Measure:    ReferenceMetricDurationMinutes,
					StartEvent: "ticket.started",
					EndEvent:   "ticket.resolved", Unit: "minutes",
					Dimensions: []string{"work_class", "priority", "queue"},
				},
				{
					Key: "it_sla_attainment", Name: "SLA 达成率",
					Measure:          ReferenceMetricRatio,
					SourceEvent:      "sla.met",
					DenominatorEvent: "sla.completed", Unit: "percent",
					Dimensions: []string{"work_class", "priority"},
				},
			},
			KnowledgeTemplates: []ReferenceKnowledgeTemplate{
				{
					Key: "it_runbooks", Name: "IT/SRE 运行手册",
					CollectionKey: "it_runbooks",
					SeedArticleTitles: []string{
						"生产事件分级与升级",
						"服务恢复与回退检查清单",
						"重大事件复盘模板",
					},
					ReaderRoles: []models.ProjectRole{
						models.ProjectRoleAdmin,
						models.ProjectRoleManager,
						models.ProjectRoleAgent,
					},
				},
			},
			ConnectorTemplates: []ReferenceConnectorTemplate{
				{
					Key: "it_monitoring", Name: "监控告警连接器",
					Kind:      ReferenceConnectorMonitoring,
					Direction: ReferenceConnectorBidirectional,
					EventTypes: []string{
						"monitoring.alert.opened",
						"ticket.resolved",
					},
					RequiredScopes: []string{
						models.ScopeTicketsCreate,
						models.ScopeTicketsRead,
						models.ScopeEventsSubscribe,
					},
				},
				{
					Key: "it_chat", Name: "协作群连接器",
					Kind:      ReferenceConnectorChat,
					Direction: ReferenceConnectorBidirectional,
					EventTypes: []string{
						"ticket.escalated",
						"ticket.comment.created",
					},
					RequiredScopes: []string{
						models.ScopeTicketsRead,
						models.ScopeCommentsWrite,
						models.ScopeEventsSubscribe,
					},
				},
			},
		},
	}
}

func hrAdminReferenceSpecification() referenceSolutionSpecification {
	return referenceSolutionSpecification{
		ID:         ReferenceSolutionHRAdminID,
		PackageKey: ReferenceSolutionHRAdminPackageKey,
		Name:       "HR/行政共享服务",
		Industry:   "shared-services",
		Prefix:     "hr",
		Terminology: map[string]string{
			"ticket":       "员工服务单",
			"requester":    "员工",
			"agent":        "共享服务专员",
			"queue":        "服务中心",
			"incident":     "员工事件",
			"service":      "员工请求",
			"consultation": "政策咨询",
			"complaint":    "员工申诉",
			"approval":     "主管审批",
		},
		RequestTypes: []referenceRequestTypeSpecification{
			{
				Key:         "hr_employee_service",
				Name:        "员工服务请求",
				Description: "入转调离、证明、福利与行政服务",
				WorkClass:   models.WorkClassRequest,
				ExtraFields: []referenceSchemaField{
					{
						Key: "employee_id", Title: "员工编号",
						Type: "string", Required: true,
					},
					{
						Key: "service_category", Title: "服务类别",
						Type: "string", Required: true,
						Enum: []string{
							"onboarding", "transfer", "offboarding",
							"benefits", "certificate", "workplace",
						},
					},
				},
			},
			{
				Key:         "hr_policy_consultation",
				Name:        "人事政策咨询",
				Description: "制度、假勤、薪酬福利与员工关系咨询",
				WorkClass:   models.WorkClassConsultation,
				ExtraFields: []referenceSchemaField{
					{
						Key: "policy_area", Title: "政策领域",
						Type: "string", Required: true,
					},
					{
						Key: "confidential", Title: "是否保密",
						Type: "boolean", Required: true,
					},
				},
			},
			{
				Key:         "hr_employee_complaint",
				Name:        "员工申诉",
				Description: "工作环境、服务体验与员工关系申诉",
				WorkClass:   models.WorkClassComplaint,
				ExtraFields: []referenceSchemaField{
					{
						Key: "complaint_category", Title: "申诉类别",
						Type: "string", Required: true,
					},
					{
						Key: "confidential", Title: "是否保密",
						Type: "boolean", Required: true,
					},
				},
			},
		},
		ResponseMinutes:   240,
		ResolutionMinutes: 2880,
		RiskWorkClass:     models.WorkClassComplaint,
		Blueprint: ReferenceSolutionBlueprint{
			AgentTemplates: []ReferenceAgentTemplate{
				{
					Key: "hr_service_agent", Name: "员工服务 Agent",
					Purpose:     "识别员工服务类别、检索政策并起草非敏感答复",
					ProjectRole: models.ProjectRoleAgent,
					AllowedActions: []ReferenceAgentAction{
						ReferenceAgentClassify,
						ReferenceAgentSummarize,
						ReferenceAgentRetrieveKnowledge,
						ReferenceAgentDraftReply,
					},
					KnowledgeCollectionKeys: []string{"hr_policy_library"},
					RequiresHumanApproval:   true,
				},
			},
			MetricTemplates: []ReferenceMetricTemplate{
				{
					Key: "hr_first_response", Name: "员工首次响应时间",
					Measure:    ReferenceMetricDurationMinutes,
					StartEvent: "ticket.created",
					EndEvent:   "ticket.first_response", Unit: "minutes",
					Dimensions: []string{"request_type", "priority"},
				},
				{
					Key: "hr_resolution_volume", Name: "员工服务完成量",
					Measure:     ReferenceMetricCount,
					SourceEvent: "ticket.resolved", Unit: "count",
					Dimensions: []string{"request_type", "work_class"},
				},
			},
			KnowledgeTemplates: []ReferenceKnowledgeTemplate{
				{
					Key: "hr_policy_library", Name: "员工政策知识库",
					CollectionKey: "hr_policy_library",
					SeedArticleTitles: []string{
						"员工入转调离服务指南",
						"假勤与福利常见问题",
						"保密申诉处理规范",
					},
					ReaderRoles: []models.ProjectRole{
						models.ProjectRoleAdmin,
						models.ProjectRoleManager,
						models.ProjectRoleAgent,
						models.ProjectRoleRequester,
					},
				},
			},
			ConnectorTemplates: []ReferenceConnectorTemplate{
				{
					Key: "hr_hris", Name: "HRIS 连接器",
					Kind:      ReferenceConnectorHRIS,
					Direction: ReferenceConnectorBidirectional,
					EventTypes: []string{
						"hr.employee.changed",
						"ticket.resolved",
					},
					RequiredScopes: []string{
						models.ScopeTicketsRead,
						models.ScopeTicketsCreate,
						models.ScopeEventsSubscribe,
					},
				},
				{
					Key: "hr_email", Name: "员工服务邮箱连接器",
					Kind:      ReferenceConnectorEmail,
					Direction: ReferenceConnectorInbound,
					EventTypes: []string{
						"email.received",
					},
					RequiredScopes: []string{
						models.ScopeTicketsCreate,
						models.ScopeCommentsWrite,
					},
				},
			},
		},
	}
}

func financeProcurementReferenceSpecification() referenceSolutionSpecification {
	return referenceSolutionSpecification{
		ID:         ReferenceSolutionFinanceProcurementID,
		PackageKey: ReferenceSolutionFinanceProcurementPackageKey,
		Name:       "财务/采购共享服务",
		Industry:   "finance-procurement",
		Prefix:     "fin",
		Terminology: map[string]string{
			"ticket":       "财采服务单",
			"requester":    "申请人",
			"agent":        "财采专员",
			"queue":        "财采中心",
			"service":      "采购申请",
			"problem":      "票据异常",
			"consultation": "财务咨询",
			"approval":     "财务审批",
			"risk":         "合规风险",
		},
		RequestTypes: []referenceRequestTypeSpecification{
			{
				Key:         "fin_procurement_request",
				Name:        "采购申请",
				Description: "商品、服务与供应商采购申请",
				WorkClass:   models.WorkClassRequest,
				ExtraFields: []referenceSchemaField{
					{
						Key: "cost_center", Title: "成本中心",
						Type: "string", Required: true,
					},
					{
						Key: "amount", Title: "申请金额",
						Type: "number", Required: true,
					},
					{
						Key: "vendor_name", Title: "供应商",
						Type: "string",
					},
				},
			},
			{
				Key:         "fin_invoice_exception",
				Name:        "发票异常",
				Description: "发票校验、匹配与付款异常",
				WorkClass:   models.WorkClassProblem,
				ExtraFields: []referenceSchemaField{
					{
						Key: "invoice_number", Title: "发票号码",
						Type: "string", Required: true,
					},
					{
						Key: "amount", Title: "发票金额",
						Type: "number", Required: true,
					},
					{
						Key: "exception_type", Title: "异常类型",
						Type: "string", Required: true,
					},
				},
			},
			{
				Key:         "fin_policy_consultation",
				Name:        "财务政策咨询",
				Description: "报销、预算、付款与采购政策咨询",
				WorkClass:   models.WorkClassConsultation,
				ExtraFields: []referenceSchemaField{
					{
						Key: "policy_area", Title: "政策领域",
						Type: "string", Required: true,
						Enum: []string{
							"expense", "budget", "payment", "procurement",
						},
					},
				},
			},
		},
		ResponseMinutes:   120,
		ResolutionMinutes: 1440,
		RiskWorkClass:     models.WorkClassRequest,
		Blueprint: ReferenceSolutionBlueprint{
			AgentTemplates: []ReferenceAgentTemplate{
				{
					Key: "fin_compliance_agent", Name: "财采合规 Agent",
					Purpose:     "分类财采服务单、检索政策并提示审批与合规风险",
					ProjectRole: models.ProjectRoleAgent,
					AllowedActions: []ReferenceAgentAction{
						ReferenceAgentClassify,
						ReferenceAgentSummarize,
						ReferenceAgentRetrieveKnowledge,
						ReferenceAgentSuggestRoute,
					},
					KnowledgeCollectionKeys: []string{"fin_policy_library"},
					RequiresHumanApproval:   true,
				},
			},
			MetricTemplates: []ReferenceMetricTemplate{
				{
					Key: "fin_cycle_time", Name: "财采处理周期",
					Measure:    ReferenceMetricDurationMinutes,
					StartEvent: "ticket.created",
					EndEvent:   "ticket.resolved", Unit: "minutes",
					Dimensions: []string{"request_type", "priority"},
				},
				{
					Key: "fin_approval_volume", Name: "审批服务量",
					Measure:     ReferenceMetricCount,
					SourceEvent: "approval.completed", Unit: "count",
					Dimensions: []string{"request_type", "work_class"},
				},
			},
			KnowledgeTemplates: []ReferenceKnowledgeTemplate{
				{
					Key: "fin_policy_library", Name: "财采政策知识库",
					CollectionKey: "fin_policy_library",
					SeedArticleTitles: []string{
						"采购申请与询比价规范",
						"发票校验与异常处理",
						"费用报销和付款审批矩阵",
					},
					ReaderRoles: []models.ProjectRole{
						models.ProjectRoleAdmin,
						models.ProjectRoleManager,
						models.ProjectRoleAgent,
						models.ProjectRoleRequester,
					},
				},
			},
			ConnectorTemplates: []ReferenceConnectorTemplate{
				{
					Key: "fin_erp", Name: "ERP 财务连接器",
					Kind:      ReferenceConnectorERP,
					Direction: ReferenceConnectorBidirectional,
					EventTypes: []string{
						"erp.invoice.exception",
						"ticket.resolved",
					},
					RequiredScopes: []string{
						models.ScopeTicketsRead,
						models.ScopeTicketsCreate,
						models.ScopeEventsSubscribe,
					},
				},
				{
					Key: "fin_procurement", Name: "采购平台连接器",
					Kind:      ReferenceConnectorProcurement,
					Direction: ReferenceConnectorBidirectional,
					EventTypes: []string{
						"procurement.request.created",
						"approval.completed",
					},
					RequiredScopes: []string{
						models.ScopeTicketsRead,
						models.ScopeTicketsUpdate,
						models.ScopeEventsSubscribe,
					},
				},
			},
		},
	}
}

func buildReferenceCoreSnapshot(
	specification referenceSolutionSpecification,
) (models.IndustrySolutionSnapshot, error) {
	requestTypes := make(
		[]models.RequestTypeTemplate,
		0,
		len(specification.RequestTypes),
	)
	for _, requestType := range specification.RequestTypes {
		template, err := buildReferenceRequestType(requestType)
		if err != nil {
			return models.IndustrySolutionSnapshot{}, err
		}
		requestTypes = append(requestTypes, template)
	}
	workflow := buildReferenceWorkflow(specification.Prefix)
	calendarKey := specification.Prefix + "_business_hours"
	calendar := models.CalendarDefinition{
		Key:      calendarKey,
		Name:     specification.Name + "工作时间",
		Timezone: "Asia/Shanghai",
		Windows: []models.CalendarWindow{
			{Weekday: 1, Start: "09:00", End: "18:00"},
			{Weekday: 2, Start: "09:00", End: "18:00"},
			{Weekday: 3, Start: "09:00", End: "18:00"},
			{Weekday: 4, Start: "09:00", End: "18:00"},
			{Weekday: 5, Start: "09:00", End: "18:00"},
		},
	}
	slas := make([]models.SLAPolicyDefinition, 0, len(requestTypes))
	routes := make([]models.RouteDefinition, 0, len(requestTypes))
	for index, requestType := range requestTypes {
		expression := referenceStringExpression(
			"ticket.work_class",
			models.ExpressionOperatorEqual,
			string(requestType.WorkClass),
		)
		slas = append(slas, models.SLAPolicyDefinition{
			Key: specification.Prefix + "_" +
				string(requestType.WorkClass) + "_sla",
			Name: specification.Name + " " +
				string(requestType.WorkClass) + " SLA",
			ResponseMinutes:   specification.ResponseMinutes,
			ResolutionMinutes: specification.ResolutionMinutes,
			CalendarKey:       calendarKey,
			PauseWhen: []models.LifecycleCategory{
				models.LifecycleCategoryWaiting,
			},
			Applicability: &expression,
		})
		routes = append(routes, models.RouteDefinition{
			Key:      requestType.Key + "_route",
			Name:     requestType.Name + "默认路由",
			Priority: 100 - index,
			When:     expression,
			QueueKey: "default",
		})
	}
	approvalKey := specification.Prefix + "_risk_approval"
	riskExpression := referenceStringExpression(
		"ticket.work_class",
		models.ExpressionOperatorEqual,
		string(specification.RiskWorkClass),
	)
	highRiskExpression := referenceStringExpression(
		"ticket.risk_level",
		models.ExpressionOperatorEqual,
		"high",
	)
	urgentExpression := referenceStringExpression(
		"ticket.priority",
		models.ExpressionOperatorEqual,
		string(models.TicketPriorityUrgent),
	)
	urgentTagParameters, err := json.Marshal(map[string]string{
		"tag": specification.Prefix + "_urgent",
	})
	if err != nil {
		return models.IndustrySolutionSnapshot{}, err
	}
	notifyParameters, err := json.Marshal(map[string]string{
		"template_key": specification.Prefix + "_urgent_notice",
		"channel":      "internal",
	})
	if err != nil {
		return models.IndustrySolutionSnapshot{}, err
	}
	approvalParameters, err := json.Marshal(map[string]string{
		"policy_key": approvalKey,
	})
	if err != nil {
		return models.IndustrySolutionSnapshot{}, err
	}
	snapshot := models.IndustrySolutionSnapshot{
		RequestTypes: requestTypes,
		Workflows: []models.WorkflowTemplate{
			workflow,
		},
		SLAPolicies: slas,
		Calendars:   []models.CalendarDefinition{calendar},
		Routes:      routes,
		Automations: []models.AutomationDefinition{
			{
				Key:     specification.Prefix + "_urgent_attention",
				Name:    specification.Name + "紧急关注",
				Enabled: true,
				When:    urgentExpression,
				Actions: []models.ConfigurationAction{
					{
						Type:       models.ConfigurationActionAddTag,
						Parameters: urgentTagParameters,
					},
					{
						Type:       models.ConfigurationActionNotify,
						Parameters: notifyParameters,
					},
				},
			},
			{
				Key:     specification.Prefix + "_risk_approval_gate",
				Name:    specification.Name + "风险审批门禁",
				Enabled: true,
				When: models.TypedExpression{
					All: []models.TypedExpression{
						riskExpression,
						highRiskExpression,
					},
				},
				Actions: []models.ConfigurationAction{
					{
						Type:       models.ConfigurationActionRequireApproval,
						Parameters: approvalParameters,
					},
				},
			},
		},
		ApprovalPolicies: []models.ApprovalPolicyDefinition{
			{
				Key:               approvalKey,
				Name:              specification.Name + "风险审批",
				When:              riskExpression,
				RequiredApprovals: 1,
				ApproverRoles: []models.ProjectRole{
					models.ProjectRoleAdmin,
					models.ProjectRoleManager,
				},
			},
		},
		RiskPolicies: []models.RiskPolicyDefinition{
			{
				Key:               specification.Prefix + "_high_risk",
				Name:              specification.Name + "高风险策略",
				When:              highRiskExpression,
				Level:             models.ConfigurationRiskHigh,
				RequiresApproval:  true,
				ApprovalPolicyKey: approvalKey,
			},
		},
	}
	blueprintJSON, err := json.Marshal(specification.Blueprint)
	if err != nil {
		return models.IndustrySolutionSnapshot{}, fmt.Errorf(
			"encode reference solution blueprint: %w",
			err,
		)
	}
	snapshot.Extensions = map[string]json.RawMessage{
		ReferenceSolutionBlueprintExtensionKey: blueprintJSON,
	}
	if err := snapshot.Validate(); err != nil {
		return models.IndustrySolutionSnapshot{}, err
	}
	return snapshot, nil
}

func buildReferenceRequestType(
	specification referenceRequestTypeSpecification,
) (models.RequestTypeTemplate, error) {
	fields := []referenceSchemaField{
		{
			Key: "summary", Title: "摘要",
			Type: "string", Required: true,
		},
		{
			Key: "description", Title: "详细说明",
			Type: "string", Required: true,
		},
		{
			Key: "priority", Title: "优先级",
			Type: "string", Required: true,
			Enum: []string{"low", "normal", "high", "urgent", "critical"},
		},
		{
			Key: "risk_level", Title: "风险级别",
			Type: "string", Required: true,
			Enum: []string{"low", "medium", "high"},
		},
	}
	fields = append(fields, specification.ExtraFields...)
	properties := make(map[string]any, len(fields))
	required := make([]string, 0, len(fields))
	elements := make([]map[string]string, 0, len(fields))
	for _, field := range fields {
		if err := validateReferenceTemplateKey(
			field.Key,
			"request field",
		); err != nil {
			return models.RequestTypeTemplate{}, err
		}
		property := map[string]any{
			"type":  field.Type,
			"title": field.Title,
		}
		switch field.Type {
		case "string":
			property["maxLength"] = 2000
		case "number":
			property["minimum"] = 0
		case "boolean", "integer":
		default:
			return models.RequestTypeTemplate{}, fmt.Errorf(
				"unsupported reference schema type %q",
				field.Type,
			)
		}
		if field.Format != "" {
			property["format"] = field.Format
		}
		if len(field.Enum) > 0 {
			property["enum"] = field.Enum
		}
		properties[field.Key] = property
		if field.Required {
			required = append(required, field.Key)
		}
		elements = append(elements, map[string]string{
			"type":  "Control",
			"scope": "#/properties/" + field.Key,
		})
	}
	sort.Strings(required)
	schema, err := json.Marshal(map[string]any{
		"$schema":              models.JSONSchemaDraft202012,
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	})
	if err != nil {
		return models.RequestTypeTemplate{}, err
	}
	uiSchema, err := json.Marshal(map[string]any{
		"type":     "VerticalLayout",
		"elements": elements,
	})
	if err != nil {
		return models.RequestTypeTemplate{}, err
	}
	return models.RequestTypeTemplate{
		Key:         specification.Key,
		Name:        specification.Name,
		Description: specification.Description,
		WorkClass:   specification.WorkClass,
		JSONSchema:  schema,
		UISchema:    uiSchema,
	}, nil
}

func buildReferenceWorkflow(prefix string) models.WorkflowTemplate {
	return models.WorkflowTemplate{
		Key:         prefix + "_standard",
		Name:        strings.ToUpper(prefix) + " 标准生命周期",
		Description: "映射 ChronoDesk 六类统一生命周期",
		States: []models.WorkflowStateDefinition{
			{
				Key: "new", Name: "新建",
				LifecycleCategory: models.LifecycleCategoryNew,
				IsInitial:         true,
			},
			{
				Key: "active", Name: "处理中",
				LifecycleCategory: models.LifecycleCategoryActive,
			},
			{
				Key: "waiting", Name: "等待中",
				LifecycleCategory: models.LifecycleCategoryWaiting,
			},
			{
				Key: "resolved", Name: "已解决",
				LifecycleCategory: models.LifecycleCategoryResolved,
			},
			{
				Key: "closed", Name: "已关闭",
				LifecycleCategory: models.LifecycleCategoryClosed,
				IsTerminal:        true,
			},
			{
				Key: "cancelled", Name: "已取消",
				LifecycleCategory: models.LifecycleCategoryCancelled,
				IsTerminal:        true,
			},
		},
		Transitions: []models.WorkflowTransitionDefinition{
			{
				Key: "start", Name: "开始处理",
				From: "new", To: "active",
				Roles: referenceWorkerRoles(),
			},
			{
				Key: "wait", Name: "等待补充",
				From: "active", To: "waiting",
				Roles: referenceWorkerRoles(),
			},
			{
				Key: "resume", Name: "恢复处理",
				From: "waiting", To: "active",
				Roles: referenceWorkerRoles(),
			},
			{
				Key: "resolve", Name: "解决",
				From: "active", To: "resolved",
				Roles: referenceWorkerRoles(),
			},
			{
				Key: "close", Name: "关闭",
				From: "resolved", To: "closed",
				Roles: referenceWorkerRoles(),
			},
			{
				Key: "cancel", Name: "取消",
				From: "new", To: "cancelled",
				Roles: []models.ProjectRole{
					models.ProjectRoleAdmin,
					models.ProjectRoleManager,
				},
			},
		},
	}
}

func referenceWorkerRoles() []models.ProjectRole {
	return []models.ProjectRole{
		models.ProjectRoleAdmin,
		models.ProjectRoleManager,
		models.ProjectRoleAgent,
	}
}

func referenceStringExpression(
	field string,
	operator models.ExpressionOperator,
	value string,
) models.TypedExpression {
	encoded, _ := json.Marshal(value)
	return models.TypedExpression{
		Field:     field,
		ValueType: models.ExpressionValueString,
		Operator:  operator,
		Value:     encoded,
	}
}

func referenceCoreTemplateReferences(
	snapshot models.IndustrySolutionSnapshot,
) []models.SolutionTemplateReference {
	references := make([]models.SolutionTemplateReference, 0)
	add := func(kind models.SolutionTemplateKind, key string) {
		references = append(references, models.SolutionTemplateReference{
			Kind: kind,
			Key:  key,
		})
	}
	for _, template := range snapshot.RequestTypes {
		add(models.SolutionTemplateRequestType, template.Key)
	}
	for _, template := range snapshot.Workflows {
		add(models.SolutionTemplateWorkflow, template.Key)
	}
	for _, template := range snapshot.SLAPolicies {
		add(models.SolutionTemplateSLA, template.Key)
	}
	for _, template := range snapshot.Calendars {
		add(models.SolutionTemplateCalendar, template.Key)
	}
	for _, template := range snapshot.Routes {
		add(models.SolutionTemplateRoute, template.Key)
	}
	for _, template := range snapshot.Automations {
		add(models.SolutionTemplateAutomation, template.Key)
	}
	for _, template := range snapshot.ApprovalPolicies {
		add(models.SolutionTemplateApproval, template.Key)
	}
	for _, template := range snapshot.RiskPolicies {
		add(models.SolutionTemplateRisk, template.Key)
	}
	extensionKeys := make([]string, 0, len(snapshot.Extensions))
	for key := range snapshot.Extensions {
		extensionKeys = append(extensionKeys, key)
	}
	sort.Strings(extensionKeys)
	for _, key := range extensionKeys {
		add(models.SolutionTemplateExtension, key)
	}
	sort.Slice(references, func(i, j int) bool {
		if references[i].Kind != references[j].Kind {
			return references[i].Kind < references[j].Kind
		}
		return references[i].Key < references[j].Key
	})
	return references
}

func validateReferenceTemplateKey(key string, kind string) error {
	if !referenceTemplateKeyPattern.MatchString(key) {
		return fmt.Errorf("reference %s key %q is invalid", kind, key)
	}
	return nil
}

func validateReferenceKeyList(values []string, kind string) error {
	if len(values) == 0 || len(values) > 32 {
		return fmt.Errorf("reference %s list is invalid", kind)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateReferenceTemplateKey(value, kind); err != nil {
			return err
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("reference %s repeats %q", kind, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateReferenceTemplates[T any](
	values []T,
	key func(T) string,
	validate func(T) error,
) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validate(value); err != nil {
			return err
		}
		identity := key(value)
		if _, duplicate := seen[identity]; duplicate {
			return fmt.Errorf("duplicate reference template %q", identity)
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func referenceCanonicalDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode reference solution blueprint: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func referenceBlueprintFromSnapshot(
	snapshot models.IndustrySolutionSnapshot,
) (ReferenceSolutionBlueprint, error) {
	raw, exists := snapshot.Extensions[ReferenceSolutionBlueprintExtensionKey]
	if !exists {
		return ReferenceSolutionBlueprint{}, errors.New(
			"reference solution snapshot lacks reference blueprint extension",
		)
	}
	var blueprint ReferenceSolutionBlueprint
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&blueprint); err != nil {
		return ReferenceSolutionBlueprint{}, fmt.Errorf(
			"decode reference solution blueprint extension: %w",
			err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ReferenceSolutionBlueprint{}, errors.New(
				"reference solution blueprint extension has multiple JSON values",
			)
		}
		return ReferenceSolutionBlueprint{}, fmt.Errorf(
			"decode reference solution blueprint extension: %w",
			err,
		)
	}
	if err := blueprint.Validate(); err != nil {
		return ReferenceSolutionBlueprint{}, err
	}
	return blueprint, nil
}

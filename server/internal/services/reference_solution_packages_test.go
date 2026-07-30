package services

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestReferenceSolutionPackagesAreStableCompleteSignedAndStrict(
	t *testing.T,
) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	packages, err := BuildReferenceSolutionPackages(
		"reference-test-signer",
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := BuildReferenceSolutionPackages(
		"reference-test-signer",
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 3 || len(repeated) != 3 {
		t.Fatalf(
			"reference package counts = %d and %d",
			len(packages),
			len(repeated),
		)
	}
	expectedIDs := map[string]string{
		ReferenceSolutionITSREPackageKey:              ReferenceSolutionITSREID,
		ReferenceSolutionHRAdminPackageKey:            ReferenceSolutionHRAdminID,
		ReferenceSolutionFinanceProcurementPackageKey: ReferenceSolutionFinanceProcurementID,
	}
	for index, bundle := range packages {
		if err := bundle.Verify(publicKey); err != nil {
			t.Fatalf(
				"verify package %q: %v",
				bundle.Core.Manifest.PackageKey,
				err,
			)
		}
		if bundle.ID != expectedIDs[bundle.Core.Manifest.PackageKey] ||
			bundle.Core.Manifest.Version != ReferenceSolutionVersion ||
			bundle.Core.Manifest.ContentHash !=
				repeated[index].Core.Manifest.ContentHash ||
			bundle.BlueprintDigest != repeated[index].BlueprintDigest ||
			!bytes.Equal(bundle.Signature, repeated[index].Signature) {
			t.Fatalf(
				"unstable reference package %q",
				bundle.Core.Manifest.PackageKey,
			)
		}
		hasEmbeddedBlueprintReference := false
		for _, reference := range bundle.Core.Manifest.TemplateReferences {
			if reference.Kind == models.SolutionTemplateExtension &&
				reference.Key == ReferenceSolutionBlueprintExtensionKey {
				hasEmbeddedBlueprintReference = true
			}
		}
		if !hasEmbeddedBlueprintReference {
			t.Fatalf(
				"package %q manifest lacks blueprint extension reference",
				bundle.Core.Manifest.PackageKey,
			)
		}
		assertReferenceCoreSnapshotComplete(t, bundle.Core.Snapshot)
		assertReferenceBlueprintComplete(t, bundle.Blueprint)
		assertReferenceSolutionJSONSafe(t, bundle)

		raw, err := bundle.Export()
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(raw) {
			t.Fatalf(
				"package %q export is invalid JSON",
				bundle.Core.Manifest.PackageKey,
			)
		}
		parsed, err := ParseReferenceSolutionPackage(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := parsed.Verify(publicKey); err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		object["unexpected"] = true
		withUnknownField, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseReferenceSolutionPackage(
			withUnknownField,
		); err == nil {
			t.Fatal("strict package parser accepted an unknown field")
		}
	}

	otherPublicKey, otherPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPackages, err := BuildReferenceSolutionPackages(
		"other-reference-signer",
		otherPrivateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	for index := range packages {
		if packages[index].ID != otherPackages[index].ID ||
			packages[index].Core.Manifest.PackageKey !=
				otherPackages[index].Core.Manifest.PackageKey ||
			packages[index].Core.Manifest.Version !=
				otherPackages[index].Core.Manifest.Version ||
			packages[index].Core.Manifest.ContentHash !=
				otherPackages[index].Core.Manifest.ContentHash ||
			packages[index].BlueprintDigest !=
				otherPackages[index].BlueprintDigest {
			t.Fatalf("signer changed stable package identity at index %d", index)
		}
		if err := otherPackages[index].Verify(otherPublicKey); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceSolutionPackagesInstallThroughExistingProjectChain(
	t *testing.T,
) {
	db, project, _ := newProjectConfigurationTestDB(t)
	service, err := NewProjectConfigurationService(
		db,
		&configurationEventAppenderStub{},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := projectConfigurationTestContext(t, project)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	packages, err := BuildReferenceSolutionPackages(
		"installation-test-signer",
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, bundle := range packages {
		preview, err := service.PreviewReferenceSolutionUpgrade(
			ctx,
			bundle,
			publicKey,
		)
		if err != nil {
			t.Fatalf(
				"preview %q: %v",
				bundle.Core.Manifest.PackageKey,
				err,
			)
		}
		if preview.PackageKey != bundle.Core.Manifest.PackageKey ||
			preview.PackageVersion != ReferenceSolutionVersion ||
			!preview.Diff.Compatible {
			t.Fatalf("preview %q = %+v", bundle.Core.Manifest.PackageKey, preview)
		}
		installation, err := service.PrepareReferenceSolutionInstallation(
			ctx,
			bundle,
			publicKey,
		)
		if err != nil {
			t.Fatalf(
				"prepare %q: %v",
				bundle.Core.Manifest.PackageKey,
				err,
			)
		}
		if installation.PackageKey != bundle.Core.Manifest.PackageKey ||
			installation.PackageVersion != ReferenceSolutionVersion ||
			installation.Status != models.SolutionInstallationPending ||
			installation.ContentHash != bundle.Core.Manifest.ContentHash {
			t.Fatalf(
				"prepared %q = %+v",
				bundle.Core.Manifest.PackageKey,
				installation,
			)
		}
		var storedInstallation models.ProjectSolutionInstallation
		if err := db.First(
			&storedInstallation,
			"id = ?",
			installation.ID,
		).Error; err != nil {
			t.Fatal(err)
		}
		var persistedDiff models.ConfigurationDiff
		if err := json.Unmarshal(
			storedInstallation.UpgradeDiff,
			&persistedDiff,
		); err != nil {
			t.Fatalf(
				"decode persisted diff %q: %v",
				bundle.Core.Manifest.PackageKey,
				err,
			)
		}
		if !referenceContainsString(
			persistedDiff.Added,
			"extension:"+ReferenceSolutionBlueprintExtensionKey,
		) {
			t.Fatalf(
				"persisted diff %q lacks blueprint extension: %+v",
				bundle.Core.Manifest.PackageKey,
				persistedDiff,
			)
		}
		var persistedSnapshot models.IndustrySolutionSnapshot
		if err := json.Unmarshal(
			storedInstallation.PackageSnapshot,
			&persistedSnapshot,
		); err != nil {
			t.Fatalf(
				"decode persisted snapshot %q: %v",
				bundle.Core.Manifest.PackageKey,
				err,
			)
		}
		persistedBlueprint, err := referenceBlueprintFromSnapshot(
			persistedSnapshot,
		)
		if err != nil {
			t.Fatalf(
				"recover persisted blueprint %q: %v",
				bundle.Core.Manifest.PackageKey,
				err,
			)
		}
		if !reflect.DeepEqual(persistedBlueprint, bundle.Blueprint) {
			t.Fatalf(
				"persisted blueprint %q differs from signed blueprint",
				bundle.Core.Manifest.PackageKey,
			)
		}
		if _, err := service.SimulateSolutionInstallation(
			ctx,
			installation.ID,
		); err != nil {
			t.Fatalf(
				"simulate %q: %v",
				bundle.Core.Manifest.PackageKey,
				err,
			)
		}
		approved, err := service.ApproveSolutionInstallation(
			ctx,
			installation.ID,
		)
		if err != nil {
			t.Fatalf(
				"approve %q: %v",
				bundle.Core.Manifest.PackageKey,
				err,
			)
		}
		if approved.Status != models.SolutionInstallationActive {
			t.Fatalf(
				"approved %q status = %q",
				bundle.Core.Manifest.PackageKey,
				approved.Status,
			)
		}
	}
	var activeCount int64
	if err := db.Model(&models.ProjectSolutionInstallation{}).
		Where(
			"organization_id = ? AND project_id = ? AND status = ?",
			project.OrganizationID,
			project.ID,
			models.SolutionInstallationActive,
		).
		Count(&activeCount).Error; err != nil {
		t.Fatal(err)
	}
	var releaseCount int64
	if err := db.Model(&models.ConfigurationRelease{}).
		Where(
			"organization_id = ? AND project_id = ? AND status = ?",
			project.OrganizationID,
			project.ID,
			models.ConfigurationStatusPublished,
		).
		Count(&releaseCount).Error; err != nil {
		t.Fatal(err)
	}
	if activeCount != 3 || releaseCount != 3 {
		t.Fatalf(
			"installed reference counts: active=%d releases=%d",
			activeCount,
			releaseCount,
		)
	}
}

func TestReferenceSolutionBlueprintTamperIsRejectedBeforeInstall(
	t *testing.T,
) {
	db, project, _ := newProjectConfigurationTestDB(t)
	service, err := NewProjectConfigurationService(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := projectConfigurationTestContext(t, project)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	packages, err := BuildReferenceSolutionPackages(
		"tamper-test-signer",
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	tampered := packages[0]
	tampered.Blueprint.AgentTemplates[0].Purpose = "篡改后的用途"
	if err := tampered.Verify(publicKey); !errors.Is(
		err,
		ErrReferenceSolutionInvalid,
	) {
		t.Fatalf("tampered verification error = %v", err)
	}
	if _, err := service.PrepareReferenceSolutionInstallation(
		ctx,
		tampered,
		publicKey,
	); !errors.Is(err, ErrReferenceSolutionInvalid) {
		t.Fatalf("tampered installation error = %v", err)
	}
	var count int64
	if err := db.Model(&models.ProjectSolutionInstallation{}).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("tampered package created %d installations", count)
	}
}

func TestFindReferenceSolutionPackageUsesStablePackageKey(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	packages, err := BuildReferenceSolutionPackages("lookup-signer", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	found, ok := FindReferenceSolutionPackage(
		packages,
		ReferenceSolutionFinanceProcurementPackageKey,
	)
	if !ok ||
		found.ID != ReferenceSolutionFinanceProcurementID ||
		found.Core.Manifest.Version != ReferenceSolutionVersion {
		t.Fatalf("reference package lookup = %+v, ok = %v", found, ok)
	}
	if _, ok := FindReferenceSolutionPackage(
		packages,
		"reference.unknown",
	); ok {
		t.Fatal("unknown reference package was found")
	}
}

func assertReferenceCoreSnapshotComplete(
	t *testing.T,
	snapshot models.IndustrySolutionSnapshot,
) {
	t.Helper()
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("invalid core snapshot: %v", err)
	}
	if len(snapshot.RequestTypes) < 3 ||
		len(snapshot.Workflows) == 0 ||
		len(snapshot.SLAPolicies) < 3 ||
		len(snapshot.Calendars) == 0 ||
		len(snapshot.Routes) < 3 ||
		len(snapshot.Automations) < 2 ||
		len(snapshot.ApprovalPolicies) == 0 ||
		len(snapshot.RiskPolicies) == 0 ||
		len(snapshot.Extensions) == 0 {
		t.Fatalf("incomplete core snapshot: %+v", snapshot)
	}
	blueprint, err := referenceBlueprintFromSnapshot(snapshot)
	if err != nil {
		t.Fatalf("core snapshot lacks valid blueprint extension: %v", err)
	}
	if err := blueprint.Validate(); err != nil {
		t.Fatalf("embedded blueprint is invalid: %v", err)
	}
	foundBlueprintReference := false
	for _, reference := range referenceCoreTemplateReferences(snapshot) {
		if reference.Kind == models.SolutionTemplateExtension &&
			reference.Key == ReferenceSolutionBlueprintExtensionKey {
			foundBlueprintReference = true
		}
	}
	if !foundBlueprintReference {
		t.Fatal("core manifest references lack reference blueprint extension")
	}
	for _, requestType := range snapshot.RequestTypes {
		if !requestType.WorkClass.IsValid() ||
			!json.Valid(requestType.JSONSchema) ||
			!json.Valid(requestType.UISchema) {
			t.Fatalf("invalid request type template: %+v", requestType)
		}
		var schema struct {
			Dialect              string                     `json:"$schema"`
			Type                 string                     `json:"type"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
			AdditionalProperties bool                       `json:"additionalProperties"`
		}
		decoder := json.NewDecoder(bytes.NewReader(requestType.JSONSchema))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&schema); err != nil {
			t.Fatalf("strict schema decode %q: %v", requestType.Key, err)
		}
		if schema.Dialect != models.JSONSchemaDraft202012 ||
			schema.Type != "object" ||
			len(schema.Properties) == 0 ||
			len(schema.Required) == 0 ||
			schema.AdditionalProperties {
			t.Fatalf("request schema %q = %+v", requestType.Key, schema)
		}
		var uiSchema struct {
			Type     string `json:"type"`
			Elements []struct {
				Type  string `json:"type"`
				Scope string `json:"scope"`
			} `json:"elements"`
		}
		uiDecoder := json.NewDecoder(bytes.NewReader(requestType.UISchema))
		uiDecoder.DisallowUnknownFields()
		if err := uiDecoder.Decode(&uiSchema); err != nil {
			t.Fatalf("strict UI schema decode %q: %v", requestType.Key, err)
		}
		if uiSchema.Type != "VerticalLayout" ||
			len(uiSchema.Elements) != len(schema.Properties) {
			t.Fatalf("UI schema %q = %+v", requestType.Key, uiSchema)
		}
		for _, element := range uiSchema.Elements {
			if element.Type != "Control" ||
				!strings.HasPrefix(element.Scope, "#/properties/") {
				t.Fatalf(
					"UI schema %q has invalid element %+v",
					requestType.Key,
					element,
				)
			}
		}
	}
	categories := make(map[models.LifecycleCategory]struct{})
	for _, workflow := range snapshot.Workflows {
		for _, state := range workflow.States {
			categories[state.LifecycleCategory] = struct{}{}
		}
	}
	expected := []models.LifecycleCategory{
		models.LifecycleCategoryNew,
		models.LifecycleCategoryActive,
		models.LifecycleCategoryWaiting,
		models.LifecycleCategoryResolved,
		models.LifecycleCategoryClosed,
		models.LifecycleCategoryCancelled,
	}
	for _, category := range expected {
		if _, exists := categories[category]; !exists {
			t.Errorf("workflow lacks lifecycle category %q", category)
		}
	}
}

func assertReferenceBlueprintComplete(
	t *testing.T,
	blueprint ReferenceSolutionBlueprint,
) {
	t.Helper()
	if err := blueprint.Validate(); err != nil {
		t.Fatalf("invalid reference blueprint: %v", err)
	}
	if len(blueprint.AgentTemplates) == 0 ||
		len(blueprint.MetricTemplates) < 2 ||
		len(blueprint.KnowledgeTemplates) == 0 ||
		len(blueprint.ConnectorTemplates) < 2 {
		t.Fatalf("incomplete reference blueprint: %+v", blueprint)
	}
	for _, agent := range blueprint.AgentTemplates {
		agentType := reflect.TypeOf(agent)
		for _, forbidden := range []string{
			"Prompt",
			"SystemPrompt",
			"Script",
			"Command",
			"SourceCode",
		} {
			if _, exists := agentType.FieldByName(forbidden); exists {
				t.Errorf("agent template exposes %q", forbidden)
			}
		}
	}
}

func assertReferenceSolutionJSONSafe(
	t *testing.T,
	bundle ReferenceSolutionPackage,
) {
	t.Helper()
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(raw))
	for _, forbiddenKey := range []string{
		`"script":`,
		`"eval":`,
		`"command":`,
		`"source_code":`,
		`"prompt":`,
		`"system_prompt":`,
		`"secret":`,
		`"credential":`,
	} {
		if strings.Contains(lower, forbiddenKey) {
			t.Errorf(
				"reference package %q contains forbidden key %s",
				bundle.Core.Manifest.PackageKey,
				forbiddenKey,
			)
		}
	}
	references := append(
		[]models.SolutionTemplateReference(nil),
		bundle.Core.Manifest.TemplateReferences...,
	)
	sorted := append([]models.SolutionTemplateReference(nil), references...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		return sorted[i].Key < sorted[j].Key
	})
	if !reflect.DeepEqual(references, sorted) {
		t.Errorf(
			"reference package %q template references are unstable",
			bundle.Core.Manifest.PackageKey,
		)
	}
}

func referenceContainsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

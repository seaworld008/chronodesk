package models

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type integrationModelFixture struct {
	db         *gorm.DB
	project    Project
	definition ConnectorDefinition
	connection Connection
	mapping    MappingVersion
}

func TestPublishedIntegrationMappingIsImmutable(t *testing.T) {
	fixture := newIntegrationModelFixture(t)
	originalDigest := fixture.mapping.DefinitionDigest
	fixture.mapping.Definition = datatypes.JSON([]byte(`{"title":"$.changed"}`))
	if err := fixture.db.Save(&fixture.mapping).Error; !errors.Is(
		err,
		ErrPublishedMappingImmutable,
	) {
		t.Fatalf("update published mapping error = %v", err)
	}
	if err := fixture.db.Delete(&fixture.mapping).Error; !errors.Is(
		err,
		ErrPublishedMappingImmutable,
	) {
		t.Fatalf("delete published mapping error = %v", err)
	}
	var persisted MappingVersion
	if err := fixture.db.First(&persisted, fixture.mapping.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.DefinitionDigest != originalDigest ||
		string(persisted.Definition) != `{"title":"$.title"}` ||
		persisted.Status != MappingVersionStatusPublished {
		t.Fatalf("published mapping changed: %+v", persisted)
	}
}

func TestIntegrationModelsEnforceProjectLocalIdentities(t *testing.T) {
	fixture := newIntegrationModelFixture(t)

	duplicateDefinition := fixture.definition
	duplicateDefinition.ID = 0
	duplicateDefinition.PublicID = ""
	if err := fixture.db.Create(&duplicateDefinition).Error; err == nil {
		t.Fatal("duplicate project connector key was accepted")
	}

	duplicateConnection := fixture.connection
	duplicateConnection.ID = 0
	duplicateConnection.PublicID = ""
	if err := fixture.db.Create(&duplicateConnection).Error; err == nil {
		t.Fatal("duplicate project connection key was accepted")
	}

	duplicateMapping := fixture.mapping
	duplicateMapping.ID = 0
	duplicateMapping.PublicID = ""
	if err := fixture.db.Create(&duplicateMapping).Error; err == nil {
		t.Fatal("duplicate connection mapping version was accepted")
	}

	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	message := InboxMessage{
		ProjectID:            fixture.project.ID,
		ConnectionID:         fixture.connection.ID,
		MappingVersionID:     fixture.mapping.ID,
		ExternalMessageID:    "message-unique",
		ExternalResourceType: "case",
		ExternalResourceID:   "EXT-UNIQUE",
		SignedAt:             now,
		ReceivedAt:           now,
		ContentType:          "application/json",
		Payload:              []byte(`{"id":"EXT-UNIQUE"}`),
		PayloadDigest:        strings.Repeat("a", 64),
		SignatureDigest:      strings.Repeat("b", 64),
		Status:               InboxMessageStatusProcessing,
	}
	if err := fixture.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	duplicateMessage := message
	duplicateMessage.ID = 0
	duplicateMessage.PublicID = ""
	if err := fixture.db.Create(&duplicateMessage).Error; err == nil {
		t.Fatal("duplicate external message identity was accepted")
	}

	link := ExternalLink{
		ProjectID:            fixture.project.ID,
		ConnectionID:         fixture.connection.ID,
		ExternalResourceType: "case",
		ExternalResourceID:   "EXT-UNIQUE",
		InternalResourceType: "ticket",
		InternalResourceID:   "100",
		MappingVersionID:     fixture.mapping.ID,
		InternalVersion:      1,
		LastInboxMessageID:   message.ID,
	}
	if err := fixture.db.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	duplicateExternal := link
	duplicateExternal.ID = 0
	duplicateExternal.PublicID = ""
	duplicateExternal.InternalResourceID = "101"
	if err := fixture.db.Create(&duplicateExternal).Error; err == nil {
		t.Fatal("duplicate external link identity was accepted")
	}
	duplicateInternal := link
	duplicateInternal.ID = 0
	duplicateInternal.PublicID = ""
	duplicateInternal.ExternalResourceID = "EXT-OTHER"
	if err := fixture.db.Create(&duplicateInternal).Error; err == nil {
		t.Fatal("duplicate internal link identity was accepted")
	}

	cursor := SyncCursor{
		ProjectID:    fixture.project.ID,
		ConnectionID: fixture.connection.ID,
		Stream:       "tickets",
		Direction:    SyncDirectionInbound,
		Cursor:       "cursor-1",
		Version:      1,
	}
	if err := fixture.db.Create(&cursor).Error; err != nil {
		t.Fatal(err)
	}
	duplicateCursor := cursor
	duplicateCursor.ID = 0
	if err := fixture.db.Create(&duplicateCursor).Error; err == nil {
		t.Fatal("duplicate sync cursor identity was accepted")
	}

	run := SyncRun{
		ProjectID:    fixture.project.ID,
		ConnectionID: fixture.connection.ID,
		RunKey:       "scheduled-2026-07-30T12:00Z",
		Direction:    SyncDirectionInbound,
		Status:       SyncRunStatusPending,
	}
	if err := fixture.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	duplicateRun := run
	duplicateRun.ID = 0
	duplicateRun.PublicID = ""
	if err := fixture.db.Create(&duplicateRun).Error; err == nil {
		t.Fatal("duplicate sync run key was accepted")
	}
}

func newIntegrationModelFixture(t *testing.T) *integrationModelFixture {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared&_foreign_keys=1",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&Organization{},
		&BusinessUnit{},
		&Project{},
		&ConnectorDefinition{},
		&Connection{},
		&MappingVersion{},
		&InboxMessage{},
		&InboxReceipt{},
		&ExternalLink{},
		&SyncRun{},
		&SyncCursor{},
		&IntegrationConflict{},
		&DeadLetter{},
	); err != nil {
		t.Fatal(err)
	}
	organization := Organization{
		Slug:   "integration-model-test",
		Name:   "Integration Model Test",
		Status: OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "integration",
		Name:           "Integration",
		Status:         BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "INT",
		Name:           "Integration",
		Status:         ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	definition := ConnectorDefinition{
		ProjectID:                  project.ID,
		Key:                        "generic-webhook",
		Name:                       "Generic Webhook",
		Kind:                       "webhook",
		Direction:                  ConnectorDirectionInbound,
		Status:                     ConnectorDefinitionStatusActive,
		SignatureScheme:            "hmac-sha256",
		DefaultReplayWindowSeconds: 300,
	}
	if err := db.Create(&definition).Error; err != nil {
		t.Fatal(err)
	}
	connection := Connection{
		ProjectID:             project.ID,
		ConnectorDefinitionID: definition.ID,
		Key:                   "primary",
		Name:                  "Primary",
		Status:                ConnectionStatusActive,
		ReplayWindowSeconds:   300,
		ActorType:             ActorTypeSystem,
		ActorID:               "connector-model-test",
	}
	if err := db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	mapping := MappingVersion{
		ProjectID:     project.ID,
		ConnectionID:  connection.ID,
		Key:           "ticket-import",
		Version:       1,
		Status:        MappingVersionStatusDraft,
		TargetCommand: "ticket.create",
		Definition:    datatypes.JSON([]byte(`{"title":"$.title"}`)),
	}
	if err := db.Create(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	if err := mapping.Publish(
		SystemActor("integration-model-test"),
		time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Save(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	return &integrationModelFixture{
		db:         db,
		project:    project,
		definition: definition,
		connection: connection,
		mapping:    mapping,
	}
}

package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestConfigServiceListConfigPageIsBoundedAndStable(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	configs := make([]models.SystemConfig, 0, 152)
	for index := 0; index < 151; index++ {
		configs = append(configs, models.SystemConfig{
			Key:       fmt.Sprintf("security.directory.%03d", index),
			Value:     "false",
			ValueType: "bool",
			Category:  CategorySecurity,
			Group:     fmt.Sprintf("group-%02d", index%7),
		})
	}
	configs = append(configs, models.SystemConfig{
		Key:       "system.directory.other",
		Value:     "other",
		ValueType: "string",
		Category:  CategorySystem,
		Group:     "other",
	})
	if err := db.Create(&configs).Error; err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(db)
	request := DirectoryPageRequest{
		Page:      1,
		PageSize:  100,
		SortBy:    "group",
		SortOrder: "asc",
	}
	first, err := service.ListConfigPage(
		context.Background(),
		CategorySecurity,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Page = 2
	second, err := service.ListConfigPage(
		context.Background(),
		CategorySecurity,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 151 || first.TotalPages != 2 ||
		len(first.Items) != 100 || len(second.Items) != 51 {
		t.Fatalf("unexpected pages: first=%+v second=%+v", first, second)
	}
	seen := make(map[uint]struct{}, 151)
	for _, page := range []*DirectoryPage[models.SystemConfig]{
		first,
		second,
	} {
		for _, config := range page.Items {
			if config.Category != CategorySecurity {
				t.Fatalf("category filter leaked row: %+v", config)
			}
			if _, duplicate := seen[config.ID]; duplicate {
				t.Fatalf("config %d appears on multiple pages", config.ID)
			}
			seen[config.ID] = struct{}{}
		}
	}
	if _, err := service.ListConfigPage(
		context.Background(),
		"unknown",
		DirectoryPageRequest{
			Page:      1,
			PageSize:  25,
			SortBy:    "category",
			SortOrder: "asc",
		},
	); !errors.Is(err, ErrDirectoryListQuery) {
		t.Fatalf("invalid category error = %v", err)
	}
}

func TestConfigExportFailsClosedAtRecordAndByteLimits(t *testing.T) {
	t.Run("record limit", func(t *testing.T) {
		db := openTestDB(t)
		if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
			t.Fatal(err)
		}
		configs := make([]models.SystemConfig, 0, MaxConfigExportRecords+1)
		for index := 0; index < MaxConfigExportRecords; index++ {
			configs = append(configs, models.SystemConfig{
				Key:       fmt.Sprintf("system.export.%05d", index),
				Value:     "x",
				ValueType: "string",
				Category:  CategorySystem,
				Group:     "export",
			})
		}
		if err := db.CreateInBatches(configs, 250).Error; err != nil {
			t.Fatal(err)
		}
		data, err := NewConfigService(db).ExportConfigs("")
		if err != nil {
			t.Fatalf("exact record limit rejected: %v", err)
		}
		var exported []models.SystemConfig
		if err := json.Unmarshal(data, &exported); err != nil {
			t.Fatal(err)
		}
		if len(exported) != MaxConfigExportRecords {
			t.Fatalf("exported records = %d", len(exported))
		}

		if err := db.Create(&models.SystemConfig{
			Key:       "system.export.overflow",
			Value:     "x",
			ValueType: "string",
			Category:  CategorySystem,
			Group:     "export",
		}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := NewConfigService(db).ExportConfigs(""); !errors.Is(
			err,
			ErrConfigExportTooLarge,
		) {
			t.Fatalf("record overflow error = %v", err)
		}
	})

	t.Run("serialized byte limit", func(t *testing.T) {
		db := openTestDB(t)
		if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&models.SystemConfig{
			Key:       "system.export.large",
			Value:     strings.Repeat("x", MaxConfigExportBytes),
			ValueType: "string",
			Category:  CategorySystem,
			Group:     "export",
		}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := NewConfigService(db).ExportConfigs(""); !errors.Is(
			err,
			ErrConfigExportTooLarge,
		) {
			t.Fatalf("byte overflow error = %v", err)
		}
	})
}

func TestValidateSystemConfigKeyUsesUnicodeCodePointContract(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"system.name",
		"通知.保留天数",
		strings.Repeat("配", 100),
		"配置.é",
		"配置.e\u0301",
	} {
		if err := ValidateSystemConfigKey(key); err != nil {
			t.Errorf("ValidateSystemConfigKey(%q) = %v, want nil", key, err)
		}
	}

	for _, test := range []struct {
		name string
		key  string
	}{
		{name: "empty", key: ""},
		{name: "more than one hundred code points", key: strings.Repeat("配", 101)},
		{name: "leading whitespace", key: " 配置"},
		{name: "trailing whitespace", key: "配置\u3000"},
		{name: "control character", key: "配置\u0001键"},
		{name: "invalid UTF-8", key: string([]byte{0xff})},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateSystemConfigKey(test.key); !errors.Is(
				err,
				ErrInvalidSystemConfigKey,
			) {
				t.Fatalf(
					"ValidateSystemConfigKey(%q) = %v, want ErrInvalidSystemConfigKey",
					test.key,
					err,
				)
			}
		})
	}
}

func TestConfigServiceWritePathsRejectInvalidKeysBeforePersistence(t *testing.T) {
	db := openTestDB(t)
	service := NewConfigService(db)
	service.auditLogger = log.New(&bytes.Buffer{}, "", 0)
	invalidKey := strings.Repeat("配", 101)
	importPayload, err := json.Marshal([]models.SystemConfig{{
		Key:       invalidKey,
		Value:     "value",
		ValueType: "string",
	}})
	if err != nil {
		t.Fatalf("marshal import payload: %v", err)
	}

	tests := []struct {
		name  string
		write func() error
	}{
		{
			name: "validate",
			write: func() error {
				return service.ValidateConfig(invalidKey, "value", "string")
			},
		},
		{
			name: "set",
			write: func() error {
				return service.SetConfig(
					invalidKey,
					"value",
					"string",
					"",
					CategorySystem,
					"",
				)
			},
		},
		{
			name: "delete",
			write: func() error {
				return service.DeleteConfig(invalidKey)
			},
		},
		{
			name: "batch",
			write: func() error {
				return service.BatchUpdateConfigs([]models.SystemConfig{{
					Key:       invalidKey,
					Value:     "value",
					ValueType: "string",
				}})
			},
		},
		{
			name: "import",
			write: func() error {
				return service.ImportConfigs(importPayload)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.write(); !errors.Is(
				err,
				ErrInvalidSystemConfigKey,
			) {
				t.Fatalf(
					"write error = %v, want ErrInvalidSystemConfigKey before database access",
					err,
				)
			}
		})
	}
}

func TestConfigServiceProtectedConfigsRejectGenericWriteBypasses(
	t *testing.T,
) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatalf("migrate system configs: %v", err)
	}
	protected := []models.SystemConfig{
		{
			Key:         KeySystemVersion,
			Value:       "0.2.0",
			ValueType:   "string",
			Description: "build identity",
			Category:    CategorySystem,
			Group:       "basic",
		},
		{
			Key:         KeyAgentGlobalReadOnly,
			Value:       "false",
			ValueType:   "bool",
			Description: "runtime control",
			Category:    CategorySecurity,
			Group:       "agent",
		},
		{
			Key:         KeyAgentEmergencyStop,
			Value:       "false",
			ValueType:   "bool",
			Description: "runtime control",
			Category:    CategorySecurity,
			Group:       "agent",
		},
		{
			Key:         adminResourceVersionKeyPrefix + "test-anchor",
			Value:       "webhook/731",
			ValueType:   "string",
			Description: "administrator CAS anchor",
			Category:    CategorySecurity,
			Group:       "agent-resource-version",
			Version:     7,
		},
	}
	if err := db.Create(&protected).Error; err != nil {
		t.Fatalf("seed protected controls: %v", err)
	}

	service := NewConfigService(db)
	service.auditLogger = log.New(&bytes.Buffer{}, "", 0)
	assertProtected := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, ErrProtectedSystemConfigKey) {
			t.Fatalf(
				"error = %v, want ErrProtectedSystemConfigKey",
				err,
			)
		}
		// Existing handlers already map invalid generic configuration keys to
		// a client error. The protected sentinel deliberately preserves that
		// fail-closed compatibility.
		if !errors.Is(err, ErrInvalidSystemConfigKey) {
			t.Fatalf(
				"error = %v, want compatibility with ErrInvalidSystemConfigKey",
				err,
			)
		}
	}
	assertControlsUnchanged := func(t *testing.T) {
		t.Helper()
		for _, key := range []string{
			KeySystemVersion,
			KeyAgentGlobalReadOnly,
			KeyAgentEmergencyStop,
			adminResourceVersionKeyPrefix + "test-anchor",
		} {
			var persisted models.SystemConfig
			if err := db.Where("key = ?", key).First(&persisted).Error; err != nil {
				t.Fatalf("load protected control %q: %v", key, err)
			}
			wantValue := "false"
			wantValueType := "bool"
			if key == KeySystemVersion {
				wantValue = "0.2.0"
				wantValueType = "string"
			}
			if key == adminResourceVersionKeyPrefix+"test-anchor" {
				wantValue = "webhook/731"
				wantValueType = "string"
			}
			if persisted.Value != wantValue ||
				persisted.ValueType != wantValueType ||
				(key == adminResourceVersionKeyPrefix+"test-anchor" &&
					persisted.Version != 7) {
				t.Fatalf(
					"protected config %q changed: %+v",
					key,
					persisted,
				)
			}
		}
	}

	for _, key := range []string{
		KeySystemVersion,
		KeyAgentGlobalReadOnly,
		KeyAgentEmergencyStop,
		adminResourceVersionKeyPrefix + "test-anchor",
	} {
		t.Run("single-key/"+key, func(t *testing.T) {
			assertProtected(t, service.ValidateConfig(key, "changed", "string"))
			assertProtected(t, service.SetConfig(
				key,
				"changed",
				"string",
				"generic bypass",
				CategorySystem,
				"basic",
			))
			assertControlsUnchanged(t)
		})

		t.Run("delete/"+key, func(t *testing.T) {
			assertProtected(t, service.DeleteConfig(key))
			assertControlsUnchanged(t)
		})
	}

	t.Run("batch is atomic", func(t *testing.T) {
		err := service.BatchUpdateConfigs([]models.SystemConfig{
			{
				Key:       "system.safe-batch",
				Value:     "created",
				ValueType: "string",
				Category:  CategorySystem,
			},
			{
				Key:       KeySystemVersion,
				Value:     "changed",
				ValueType: "string",
				Category:  CategorySystem,
			},
		})
		assertProtected(t, err)
		assertControlsUnchanged(t)
		var count int64
		if err := db.Model(&models.SystemConfig{}).
			Where("key = ?", "system.safe-batch").
			Count(&count).Error; err != nil {
			t.Fatalf("count ordinary batch row: %v", err)
		}
		if count != 0 {
			t.Fatal("rejected batch persisted an ordinary row")
		}
	})

	t.Run("import is atomic", func(t *testing.T) {
		payload, err := json.Marshal([]models.SystemConfig{
			{
				Key:       "system.safe-import",
				Value:     "created",
				ValueType: "string",
				Category:  CategorySystem,
			},
			{
				Key:       KeySystemVersion,
				Value:     "changed",
				ValueType: "string",
				Category:  CategorySystem,
			},
		})
		if err != nil {
			t.Fatalf("marshal import: %v", err)
		}
		assertProtected(t, service.ImportConfigs(payload))
		assertControlsUnchanged(t)
		var count int64
		if err := db.Model(&models.SystemConfig{}).
			Where("key = ?", "system.safe-import").
			Count(&count).Error; err != nil {
			t.Fatalf("count ordinary import row: %v", err)
		}
		if count != 0 {
			t.Fatal("rejected import persisted an ordinary row")
		}
	})
}

func TestConfigServiceGenericListAndExportHideProtectedConfigs(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatalf("migrate system configs: %v", err)
	}
	configs := []models.SystemConfig{
		{
			Key:       "security.visible",
			Value:     "true",
			ValueType: "bool",
			Category:  CategorySecurity,
			Group:     "visible",
		},
		{
			Key:       KeySystemVersion,
			Value:     "0.2.0",
			ValueType: "string",
			Category:  CategorySystem,
			Group:     "basic",
		},
		{
			Key:       KeyAgentGlobalReadOnly,
			Value:     "true",
			ValueType: "bool",
			Category:  CategorySecurity,
			Group:     "agent",
		},
		{
			Key:       KeyAgentEmergencyStop,
			Value:     "true",
			ValueType: "bool",
			Category:  CategorySecurity,
			Group:     "agent",
		},
		{
			Key:       adminResourceVersionKeyPrefix + "list-anchor",
			Value:     "webhook/731",
			ValueType: "string",
			Category:  CategorySecurity,
			Group:     "agent-resource-version",
			Version:   9,
		},
		{
			Key:       "agent.resourceXversion.visible",
			Value:     "ordinary",
			ValueType: "string",
			Category:  CategorySecurity,
			Group:     "visible",
		},
	}
	if err := db.Create(&configs).Error; err != nil {
		t.Fatalf("seed configs: %v", err)
	}
	service := NewConfigService(db)

	page, err := service.ListConfigPage(
		context.Background(),
		CategorySecurity,
		DirectoryPageRequest{
			Page:      1,
			PageSize:  25,
			SortBy:    "key",
			SortOrder: "asc",
		},
	)
	if err != nil {
		t.Fatalf("list generic configs: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 2 ||
		page.Items[0].Key != "agent.resourceXversion.visible" ||
		page.Items[1].Key != "security.visible" {
		t.Fatalf("generic list exposed protected controls: %+v", page)
	}

	data, err := service.ExportConfigs(CategorySecurity)
	if err != nil {
		t.Fatalf("export generic configs: %v", err)
	}
	var exported []models.SystemConfig
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if len(exported) != 2 ||
		exported[0].Key != "agent.resourceXversion.visible" ||
		exported[1].Key != "security.visible" {
		t.Fatalf("generic export exposed protected controls: %+v", exported)
	}

	systemData, err := service.ExportConfigs(CategorySystem)
	if err != nil {
		t.Fatalf("export system configs: %v", err)
	}
	if err := json.Unmarshal(systemData, &exported); err != nil {
		t.Fatalf("decode system export: %v", err)
	}
	if len(exported) != 0 {
		t.Fatalf("system export exposed build identity: %+v", exported)
	}
}

func TestConfigServiceGenericGetHidesAdministratorVersionAnchor(
	t *testing.T,
) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	const suffix = "generic-get"
	key := adminResourceVersionKeyPrefix + suffix
	if err := db.Create(&models.SystemConfig{
		Key:       key,
		Value:     "webhook/731",
		ValueType: "string",
		Category:  CategorySecurity,
		Version:   4,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if value, err := NewConfigService(db).GetConfig(key); !errors.Is(err, ErrProtectedSystemConfigKey) || value != "" {
		t.Fatalf(
			"generic GET exposed administrator anchor value=%q err=%v",
			value,
			err,
		)
	}
}

func TestConfigImportIgnoresClientIdentityAndCannotOverwriteProtectedRows(
	t *testing.T,
) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	protected := []models.SystemConfig{
		{
			Key:       KeySystemVersion,
			Value:     "0.2.0",
			ValueType: "string",
			Category:  CategorySystem,
			Version:   11,
		},
		{
			Key:       adminResourceVersionKeyPrefix + "import-anchor",
			Value:     "webhook/731",
			ValueType: "string",
			Category:  CategorySecurity,
			Version:   12,
		},
	}
	if err := db.Create(&protected).Error; err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal([]models.SystemConfig{
		{
			ID:          protected[0].ID,
			Key:         "system.safe-import-one",
			Value:       "one",
			ValueType:   "string",
			Description: "safe imported config",
			Category:    CategorySystem,
			Group:       "import",
			Version:     999,
		},
		{
			ID:          protected[1].ID,
			Key:         "system.safe-import-two",
			Value:       "two",
			ValueType:   "string",
			Description: "safe imported config",
			Category:    CategorySystem,
			Group:       "import",
			Version:     999,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(db)
	service.auditLogger = log.New(&bytes.Buffer{}, "", 0)
	if err := service.ImportConfigs(payload); err != nil {
		t.Fatalf("import ordinary keys with hostile identities: %v", err)
	}
	for index := range protected {
		var persisted models.SystemConfig
		if err := db.First(&persisted, protected[index].ID).Error; err != nil {
			t.Fatal(err)
		}
		if persisted.Key != protected[index].Key ||
			persisted.Value != protected[index].Value ||
			persisted.Version != protected[index].Version {
			t.Fatalf(
				"protected row %d changed through import identity: %+v",
				protected[index].ID,
				persisted,
			)
		}
	}
	for _, key := range []string{
		"system.safe-import-one",
		"system.safe-import-two",
	} {
		var imported models.SystemConfig
		if err := db.Where("key = ?", key).First(&imported).Error; err != nil {
			t.Fatalf("load imported key %q: %v", key, err)
		}
		if imported.ID == protected[0].ID ||
			imported.ID == protected[1].ID ||
			imported.Version == 999 {
			t.Fatalf(
				"import trusted client persistence identity for %q: %+v",
				key,
				imported,
			)
		}
	}
}

func TestConfigExportImportRoundTripUpdatesOnlyMutableFields(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(
		2026,
		time.August,
		1,
		8,
		0,
		0,
		0,
		time.UTC,
	)
	updatedAt := createdAt.Add(time.Hour)
	owner := uint(71)
	minValue := 1
	maxValue := 9
	config := models.SystemConfig{
		Key:          "system.roundtrip-safe",
		Value:        "canonical",
		ValueType:    "string",
		Description:  "canonical description",
		Category:     CategorySystem,
		Group:        "roundtrip",
		IsRequired:   true,
		IsActive:     true,
		DefaultValue: "default",
		MinValue:     &minValue,
		MaxValue:     &maxValue,
		ValidValues:  `["canonical","default"]`,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		UpdatedBy:    &owner,
		Version:      7,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(db)
	service.auditLogger = log.New(&bytes.Buffer{}, "", 0)
	exported, err := service.ExportConfigs(CategorySystem)
	if err != nil {
		t.Fatal(err)
	}

	managedBy := uint(72)
	serverUpdatedAt := updatedAt.Add(2 * time.Hour)
	if err := db.Model(&models.SystemConfig{}).
		Where("id = ?", config.ID).
		Updates(map[string]any{
			"value":       "server-drift",
			"description": "server drift",
			"version":     42,
			"updated_by":  managedBy,
			"updated_at":  serverUpdatedAt,
		}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.ImportConfigs(exported); err != nil {
		t.Fatalf("round-trip import: %v", err)
	}

	var restored models.SystemConfig
	if err := db.First(&restored, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restored.ID != config.ID ||
		restored.Key != config.Key ||
		restored.Value != config.Value ||
		restored.ValueType != config.ValueType ||
		restored.Description != config.Description ||
		restored.Category != config.Category ||
		restored.Group != config.Group ||
		restored.IsRequired != config.IsRequired ||
		restored.IsActive != config.IsActive ||
		restored.DefaultValue != config.DefaultValue ||
		restored.MinValue == nil ||
		*restored.MinValue != minValue ||
		restored.MaxValue == nil ||
		*restored.MaxValue != maxValue ||
		restored.ValidValues != config.ValidValues {
		t.Fatalf("round-trip mutable fields drifted: %+v", restored)
	}
	if restored.Version != 42 ||
		restored.UpdatedBy == nil ||
		*restored.UpdatedBy != managedBy ||
		!restored.CreatedAt.Equal(createdAt) ||
		restored.UpdatedAt.Equal(updatedAt) {
		t.Fatalf(
			"round-trip import trusted client persistence identity: %+v",
			restored,
		)
	}
}

func TestConfigServiceReadsCommittedUpdatesAcrossInstancesImmediately(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatalf("migrate system configs: %v", err)
	}

	writer := NewConfigService(db)
	reader := NewConfigService(db)
	writer.auditLogger = log.New(&bytes.Buffer{}, "", 0)
	reader.auditLogger = log.New(&bytes.Buffer{}, "", 0)

	const key = "system.test.immediate-consistency"
	if err := writer.SetConfig(
		key,
		"before",
		"string",
		"consistency test",
		CategorySystem,
		"test",
	); err != nil {
		t.Fatalf("create config: %v", err)
	}
	if got, err := reader.GetConfig(key); err != nil || got != "before" {
		t.Fatalf("initial read = %q, %v; want before", got, err)
	}

	if err := writer.SetConfig(
		key,
		"after",
		"string",
		"consistency test",
		CategorySystem,
		"test",
	); err != nil {
		t.Fatalf("update config: %v", err)
	}
	if got, err := reader.GetConfig(key); err != nil || got != "after" {
		t.Fatalf("read after update = %q, %v; want after", got, err)
	}
}

func TestConfigAuditLogStoresDigestNotPlaintextValue(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatalf("migrate system configs: %v", err)
	}

	var output bytes.Buffer
	service := NewConfigService(db)
	service.auditLogger = log.New(&output, "", 0)

	const (
		key       = "system.test.audit-redaction"
		sensitive = "plaintext-value-must-not-appear"
	)
	if err := service.SetConfig(
		key,
		sensitive,
		"string",
		"audit redaction test",
		CategorySystem,
		"test",
	); err != nil {
		t.Fatalf("create config: %v", err)
	}

	logged := output.String()
	if strings.Contains(logged, sensitive) {
		t.Fatalf("configuration audit log leaked plaintext: %q", logged)
	}
	for _, required := range []string{
		"operation=CREATE",
		"key=" + key,
		"value_sha256=",
	} {
		if !strings.Contains(logged, required) {
			t.Fatalf("configuration audit log %q is missing %q", logged, required)
		}
	}
}

func TestConfigAuditLogCannotForgeAdditionalEntries(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatalf("migrate system configs: %v", err)
	}

	var output bytes.Buffer
	service := NewConfigService(db)
	service.auditLogger = log.New(&output, "", 0)

	service.logConfigChange(
		"system.test\r\n[ERROR] forged-key\u202e",
		"credential-value-must-not-appear",
		"UPDATE\r\n[ERROR] forged-operation",
	)

	logged := output.String()
	if strings.Count(logged, "\n") != 1 || strings.Contains(logged, "\r") {
		t.Fatalf("configuration audit log contains forged line boundaries: %q", logged)
	}
	if strings.Contains(logged, "\u202e") {
		t.Fatalf("configuration audit log contains display-control character: %q", logged)
	}
	for _, forbidden := range []string{
		"credential-value-must-not-appear",
		"\n[ERROR]",
	} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("configuration audit log contains unsafe value %q: %q", forbidden, logged)
		}
	}
}

func TestBatchConfigAuditLogsOnlyCommittedChanges(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatalf("migrate system configs: %v", err)
	}

	var output bytes.Buffer
	service := NewConfigService(db)
	service.auditLogger = log.New(&output, "", 0)
	if err := db.Exec(`
		CREATE TRIGGER reject_batch_config_conflict
		BEFORE INSERT ON system_configs
		WHEN NEW.key = 'system.test.batch-conflict'
		BEGIN
			SELECT RAISE(ABORT, 'injected batch conflict');
		END
	`).Error; err != nil {
		t.Fatalf("install batch rollback trigger: %v", err)
	}

	err := service.BatchUpdateConfigs([]models.SystemConfig{
		{
			Key:       "system.test.batch-first",
			Value:     "first",
			ValueType: "string",
			Category:  CategorySystem,
		},
		{
			Key:       "system.test.batch-conflict",
			Value:     "second",
			ValueType: "string",
			Category:  CategorySystem,
		},
	})
	if err == nil {
		t.Fatal("batch with conflicting primary key unexpectedly succeeded")
	}
	if output.Len() != 0 {
		t.Fatalf("rolled-back batch produced audit output: %q", output.String())
	}

	var count int64
	if err := db.Model(&models.SystemConfig{}).Count(&count).Error; err != nil {
		t.Fatalf("count configs: %v", err)
	}
	if count != 0 {
		t.Fatalf("rolled-back batch left %d rows", count)
	}
}

package services

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

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

	err := service.BatchUpdateConfigs([]models.SystemConfig{
		{
			Key:       "system.test.batch-first",
			Value:     "first",
			ValueType: "string",
			Category:  CategorySystem,
		},
		{
			ID:        1,
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

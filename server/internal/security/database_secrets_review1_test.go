package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestDatabaseSecretRotationSQLiteA2AJSONNullAndOldAuthentication(
	t *testing.T,
) {
	db := openSecretTestDB(t)
	scope := defaultSecretTestScope(t, db)
	oldRing := testDatabaseKeyring(t, "dek-old", 0x73)
	rotating := newSnapshotTestKeyring(t, "dek-new", map[string]byte{
		"dek-old": 0x73,
		"dek-new": 0x74,
	})
	newOnly := testDatabaseKeyring(t, "dek-new", 0x74)

	tokenOnlyID := "sqlite-token-only"
	tokenEnvelope, err := oldRing.Seal(
		[]byte("sqlite-token-secret"),
		FieldAAD(a2aPushSecretsTable, tokenOnlyID, "token"),
	)
	if err != nil {
		t.Fatal(err)
	}
	tokenOnly := models.AgentPushNotificationConfig{
		ID:             tokenOnlyID,
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		TaskID:         "sqlite-token-task",
		URL:            "https://push.example.test/token",
		Token:          tokenEnvelope,
	}
	if err := db.Create(&tokenOnly).Error; err != nil {
		t.Fatal(err)
	}
	var authenticationType string
	if err := db.Raw(
		`SELECT typeof(authentication)
		   FROM agent_push_notification_configs
		  WHERE id = ?`,
		tokenOnlyID,
	).Scan(&authenticationType).Error; err != nil {
		t.Fatal(err)
	}
	if authenticationType != "null" {
		t.Fatalf(
			"token-only authentication storage = %q, want SQL NULL",
			authenticationType,
		)
	}

	authenticationOnlyID := "sqlite-authentication-only"
	authenticationEnvelope, err := oldRing.Seal(
		[]byte("sqlite-authentication-secret"),
		FieldAAD(
			a2aPushSecretsTable,
			authenticationOnlyID,
			"authentication",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	encodedAuthentication, err := json.Marshal(authenticationEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	authenticationOnly := models.AgentPushNotificationConfig{
		ID:             authenticationOnlyID,
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		TaskID:         "sqlite-authentication-task",
		URL:            "https://push.example.test/authentication",
		Authentication: datatypes.JSON(encodedAuthentication),
	}
	if err := db.Create(&authenticationOnly).Error; err != nil {
		t.Fatal(err)
	}

	if err := ValidateDatabaseSecrets(
		context.Background(),
		db,
		newOnly,
	); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("new-only validation before A2A rotation error = %v", err)
	}
	report, err := RotateDatabaseSecrets(context.Background(), db, rotating)
	if err != nil {
		t.Fatal(err)
	}
	if report.Rotated != 2 {
		t.Fatalf("A2A rotation report = %+v, want rotated=2", report)
	}
	if err := ValidateDatabaseSecrets(
		context.Background(),
		db,
		newOnly,
	); err != nil {
		t.Fatalf("new-only validation after A2A rotation failed: %v", err)
	}
}

func TestDatabaseSecretErrorsDoNotAssociateRecordAndKeyMetadata(t *testing.T) {
	const oldKeyID = "dek-sensitive-old"
	tests := []struct {
		name  string
		setup func(*testing.T, *gorm.DB, models.ProjectScope, Protector) []string
	}{
		{
			name: "webhook_config",
			setup: func(
				t *testing.T,
				db *gorm.DB,
				scope models.ProjectScope,
				oldRing Protector,
			) []string {
				row := models.WebhookConfig{
					OrganizationID: scope.OrganizationID,
					ProjectID:      scope.ProjectID,
					Name:           "redaction-config-record",
					Provider:       models.WebhookProviderCustom,
					WebhookURL:     "https://redaction.example.test/config",
					Status:         models.WebhookStatusActive,
					CreatedBy:      1,
				}
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
				plaintext := "redaction-config-plaintext"
				envelope, err := oldRing.Seal(
					[]byte(plaintext),
					FieldAAD(
						webhookSecretsTable,
						strconv.FormatUint(uint64(row.ID), 10),
						"secret",
					),
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&row).
					UpdateColumn("secret", envelope).Error; err != nil {
					t.Fatal(err)
				}
				return []string{
					oldKeyID,
					envelope,
					plaintext,
					row.WebhookURL,
					"redaction-config-record",
				}
			},
		},
		{
			name: "webhook_snapshot",
			setup: func(
				t *testing.T,
				db *gorm.DB,
				scope models.ProjectScope,
				oldRing Protector,
			) []string {
				var project models.Project
				if err := db.First(&project, scope.ProjectID).Error; err != nil {
					t.Fatal(err)
				}
				config := createSnapshotSecretWebhook(t, db, project)
				plaintext := "redaction-snapshot-plaintext"
				envelope := sealSnapshotField(
					t,
					oldRing,
					config.ID,
					"secret",
					plaintext,
				)
				snapshot := createSnapshotSecretRow(
					t,
					db,
					config,
					time.Now().UTC().Add(time.Hour),
					envelope,
					"",
					"",
				)
				return []string{
					oldKeyID,
					envelope,
					plaintext,
					snapshot.ID,
					config.WebhookURL,
				}
			},
		},
		{
			name: "a2a_push",
			setup: func(
				t *testing.T,
				db *gorm.DB,
				scope models.ProjectScope,
				oldRing Protector,
			) []string {
				const rowID = "redaction-a2a-record"
				plaintext := "redaction-a2a-token"
				envelope, err := oldRing.Seal(
					[]byte(plaintext),
					FieldAAD(a2aPushSecretsTable, rowID, "token"),
				)
				if err != nil {
					t.Fatal(err)
				}
				row := models.AgentPushNotificationConfig{
					ID:             rowID,
					OrganizationID: scope.OrganizationID,
					ProjectID:      scope.ProjectID,
					TaskID:         "redaction-a2a-task",
					URL:            "https://redaction.example.test/a2a",
					Token:          envelope,
				}
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
				return []string{
					oldKeyID,
					envelope,
					plaintext,
					rowID,
					row.URL,
				}
			},
		},
		{
			name: "email",
			setup: func(
				t *testing.T,
				db *gorm.DB,
				_ models.ProjectScope,
				oldRing Protector,
			) []string {
				row := models.EmailConfig{
					SMTPHost: "redaction.smtp.example.test",
					SMTPPort: 587,
				}
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
				plaintext := "redaction-email-password"
				envelope, err := oldRing.Seal(
					[]byte(plaintext),
					FieldAAD(
						emailSecretsTable,
						strconv.FormatUint(uint64(row.ID), 10),
						"smtp_password",
					),
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&row).
					UpdateColumn("smtp_password", envelope).Error; err != nil {
					t.Fatal(err)
				}
				return []string{
					oldKeyID,
					envelope,
					plaintext,
					fmt.Sprintf("%d", row.ID),
					row.SMTPHost,
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openSecretTestDB(t)
			scope := defaultSecretTestScope(t, db)
			oldRing := testDatabaseKeyring(t, oldKeyID, 0x75)
			newOnly := testDatabaseKeyring(t, "dek-redaction-new", 0x76)
			forbidden := test.setup(t, db, scope, oldRing)

			err := ValidateDatabaseSecrets(
				context.Background(),
				db,
				newOnly,
			)
			if !errors.Is(err, ErrUnknownKey) {
				t.Fatalf("validation error = %v, want ErrUnknownKey", err)
			}
			errorText := err.Error()
			for _, value := range forbidden {
				if value != "" && strings.Contains(errorText, value) {
					t.Fatalf(
						"validation error contains forbidden metadata %q: %s",
						value,
						errorText,
					)
				}
			}
		})
	}
}

func TestWebhookSnapshotMaintenanceQueriesFilterLiveRowsAtSQLBoundary(
	t *testing.T,
) {
	tests := []struct {
		name string
		run  func(*testing.T, *gorm.DB, Protector, time.Time)
	}{
		{
			name: "validation",
			run: func(
				t *testing.T,
				db *gorm.DB,
				protector Protector,
				maintenanceNow time.Time,
			) {
				if err := validateDatabaseSecretsAt(
					context.Background(),
					db,
					protector,
					maintenanceNow,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "rotation",
			run: func(
				t *testing.T,
				db *gorm.DB,
				protector Protector,
				maintenanceNow time.Time,
			) {
				if _, err := rotateDatabaseSecretsAt(
					context.Background(),
					db,
					protector,
					maintenanceNow,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, project := openSnapshotSecretTestDB(t)
			maintenanceNow := time.Date(
				2026,
				8,
				10,
				18,
				0,
				0,
				0,
				time.UTC,
			)
			config := createSnapshotSecretWebhook(t, db, project)
			createSnapshotSecretRow(
				t,
				db,
				config,
				maintenanceNow.Add(-time.Hour),
				"",
				"",
				"",
			)

			var snapshotQueries []string
			callbackName := "capture_" + test.name + "_snapshot_query"
			if err := db.Callback().Query().After("gorm:query").Register(
				callbackName,
				func(query *gorm.DB) {
					tableName := query.Statement.Table
					if query.Statement.Schema != nil {
						tableName = query.Statement.Schema.Table
					}
					sqlText := query.Statement.SQL.String()
					if tableName == "webhook_delivery_snapshots" &&
						strings.Contains(sqlText, "credential_expires_at") {
						snapshotQueries = append(snapshotQueries, sqlText)
					}
				},
			); err != nil {
				t.Fatal(err)
			}
			defer db.Callback().Query().Remove(callbackName)

			test.run(
				t,
				db,
				testDatabaseKeyring(t, "dek-query-boundary", 0x7d),
				maintenanceNow,
			)
			if len(snapshotQueries) != 1 {
				t.Fatalf(
					"snapshot envelope queries = %d, want one live-only query",
					len(snapshotQueries),
				)
			}
			normalized := strings.ToLower(snapshotQueries[0])
			if !strings.Contains(
				normalized,
				"credential_shredded_at is null",
			) || !strings.Contains(
				normalized,
				"credential_expires_at >",
			) {
				t.Fatalf(
					"snapshot query is not live-only: %s",
					snapshotQueries[0],
				)
			}
		})
	}
}

func TestWebhookConfigCASRejectsChangedGeneration(t *testing.T) {
	db := openSecretTestDB(t)
	scope := defaultSecretTestScope(t, db)
	oldRing := testDatabaseKeyring(t, "dek-old", 0x77)
	rotating := newSnapshotTestKeyring(t, "dek-new", map[string]byte{
		"dek-old": 0x77,
		"dek-new": 0x78,
	})
	row := models.WebhookConfig{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "config-cas-record",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://cas.example.test/config",
		Status:         models.WebhookStatusActive,
		CreatedBy:      1,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	row.Secret = sealSnapshotField(
		t,
		oldRing,
		row.ID,
		"secret",
		"stale-config-secret",
	)
	if err := db.Model(&row).UpdateColumn("secret", row.Secret).Error; err != nil {
		t.Fatal(err)
	}
	stale := row
	currentAccessToken := sealSnapshotField(
		t,
		oldRing,
		row.ID,
		"access_token",
		"current-config-access-token",
	)
	if err := db.Table("webhook_configs").
		Where("id = ?", row.ID).
		UpdateColumn("access_token", currentAccessToken).Error; err != nil {
		t.Fatal(err)
	}

	report, err := rotateWebhookConfigRow(db, rotating, stale)
	if err == nil {
		t.Fatal("stale webhook config generation unexpectedly succeeded")
	}
	if report != (SecretRotationReport{}) {
		t.Fatalf("stale config report = %+v, want zero", report)
	}
	var stored models.WebhookConfig
	if err := db.Unscoped().First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Secret != stale.Secret ||
		stored.AccessToken != currentAccessToken {
		t.Fatal("stale webhook config generation was overwritten")
	}
}

func TestA2APushCASRejectsChangedTokenAndAuthenticationGenerations(
	t *testing.T,
) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *gorm.DB, models.AgentPushNotificationConfig, Protector)
	}{
		{
			name: "token",
			mutate: func(
				t *testing.T,
				db *gorm.DB,
				row models.AgentPushNotificationConfig,
				oldRing Protector,
			) {
				current, err := oldRing.Seal(
					[]byte("current-token-generation"),
					FieldAAD(a2aPushSecretsTable, row.ID, "token"),
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Table(a2aPushSecretsTable).
					Where("id = ?", row.ID).
					UpdateColumn("token", current).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "authentication",
			mutate: func(
				t *testing.T,
				db *gorm.DB,
				row models.AgentPushNotificationConfig,
				oldRing Protector,
			) {
				current, err := oldRing.Seal(
					[]byte("current-authentication-generation"),
					FieldAAD(
						a2aPushSecretsTable,
						row.ID,
						"authentication",
					),
				)
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(current)
				if err != nil {
					t.Fatal(err)
				}
				nextUpdatedAt := row.UpdatedAt.Add(time.Second)
				if err := db.Table(a2aPushSecretsTable).
					Where("id = ?", row.ID).
					Updates(map[string]any{
						"authentication": datatypes.JSON(encoded),
						"updated_at":     nextUpdatedAt,
					}).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openSecretTestDB(t)
			scope := defaultSecretTestScope(t, db)
			oldRing := testDatabaseKeyring(t, "dek-old", 0x79)
			rotating := newSnapshotTestKeyring(t, "dek-new", map[string]byte{
				"dek-old": 0x79,
				"dek-new": 0x7a,
			})
			row := models.AgentPushNotificationConfig{
				ID:             "a2a-cas-" + test.name,
				OrganizationID: scope.OrganizationID,
				ProjectID:      scope.ProjectID,
				TaskID:         "a2a-cas-task-" + test.name,
				URL:            "https://cas.example.test/a2a/" + test.name,
			}
			token, err := oldRing.Seal(
				[]byte("stale-token-generation"),
				FieldAAD(a2aPushSecretsTable, row.ID, "token"),
			)
			if err != nil {
				t.Fatal(err)
			}
			row.Token = token
			authentication, err := oldRing.Seal(
				[]byte("stale-authentication-generation"),
				FieldAAD(a2aPushSecretsTable, row.ID, "authentication"),
			)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(authentication)
			if err != nil {
				t.Fatal(err)
			}
			row.Authentication = datatypes.JSON(encoded)
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			var stale models.AgentPushNotificationConfig
			if err := db.First(&stale, "id = ?", row.ID).Error; err != nil {
				t.Fatal(err)
			}
			test.mutate(t, db, stale, oldRing)

			report, err := rotateA2APushRow(db, rotating, stale)
			if err == nil {
				t.Fatal("stale A2A push generation unexpectedly succeeded")
			}
			if report != (SecretRotationReport{}) {
				t.Fatalf("stale A2A report = %+v, want zero", report)
			}
		})
	}
}

func TestEmailCASRejectsChangedGeneration(t *testing.T) {
	db := openSecretTestDB(t)
	oldRing := testDatabaseKeyring(t, "dek-old", 0x7b)
	rotating := newSnapshotTestKeyring(t, "dek-new", map[string]byte{
		"dek-old": 0x7b,
		"dek-new": 0x7c,
	})
	row := models.EmailConfig{SMTPPort: 587}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	oldEnvelope, err := oldRing.Seal(
		[]byte("stale-email-generation"),
		FieldAAD(
			emailSecretsTable,
			strconv.FormatUint(uint64(row.ID), 10),
			"smtp_password",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&row).
		UpdateColumn("smtp_password", oldEnvelope).Error; err != nil {
		t.Fatal(err)
	}
	currentEnvelope, err := oldRing.Seal(
		[]byte("current-email-generation"),
		FieldAAD(
			emailSecretsTable,
			strconv.FormatUint(uint64(row.ID), 10),
			"smtp_password",
		),
	)
	if err != nil {
		t.Fatal(err)
	}

	var mutateOnce sync.Once
	callbackName := "mutate_email_generation_before_cas"
	if err := db.Callback().Update().Before("gorm:update").Register(
		callbackName,
		func(query *gorm.DB) {
			if query.Statement.Table != emailSecretsTable {
				return
			}
			mutateOnce.Do(func() {
				if err := query.Exec(
					`UPDATE email_configs
					    SET smtp_password = ?
					  WHERE id = ?`,
					currentEnvelope,
					row.ID,
				).Error; err != nil {
					query.AddError(err)
				}
			})
		},
	); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Update().Remove(callbackName)

	report, err := RotateDatabaseSecrets(
		context.Background(),
		db,
		rotating,
	)
	if err == nil {
		t.Fatal("stale email generation unexpectedly succeeded")
	}
	if report != (SecretRotationReport{}) {
		t.Fatalf("stale email report = %+v, want zero", report)
	}
	var stored models.EmailConfig
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.SMTPPassword != oldEnvelope {
		t.Fatal("failed email rotation did not roll back the transaction")
	}
}

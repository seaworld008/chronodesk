package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

func TestWebhookAdminCASSerializesOrdinaryAndEmergencyPostgres(
	t *testing.T,
) {
	for _, test := range []struct {
		name  string
		left  webhookAdminCASMutation
		right webhookAdminCASMutation
	}{
		{
			name:  "update versus emergency revoke",
			left:  webhookAdminCASUpdate,
			right: webhookAdminCASEmergencyRevoke,
		},
		{
			name:  "delete versus emergency revoke",
			left:  webhookAdminCASDelete,
			right: webhookAdminCASEmergencyRevoke,
		},
		{
			name:  "update versus delete",
			left:  webhookAdminCASUpdate,
			right: webhookAdminCASDelete,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWebhookOutboxLifecyclePostgresFixture(t)
			fixture.clearRows(t)
			prepareWebhookAdminCASPostgresFixture(t, fixture)
			pair := fixture.seedPair(
				t,
				fixture.projectA,
				models.OutboxDeliveryPending,
				fixture.now.Add(time.Hour),
				"",
				nil,
				0,
			)
			ctx, err := WithOperationContext(
				context.Background(),
				OperationContext{
					Scope: fixture.projectA.Scope(),
					Actor: models.HumanActor(
						postgresLifecycleEmergencyUserID,
					),
					Source: SourceProtocolHumanREST,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			results := make(chan struct {
				mutation webhookAdminCASMutation
				err      error
			}, 2)
			var workers sync.WaitGroup
			for _, contender := range []struct {
				db       *gorm.DB
				mutation webhookAdminCASMutation
			}{
				{db: fixture.runtimeA, mutation: test.left},
				{db: fixture.runtimeB, mutation: test.right},
			} {
				contender := contender
				workers.Add(1)
				go func() {
					defer workers.Done()
					<-start
					results <- struct {
						mutation webhookAdminCASMutation
						err      error
					}{
						mutation: contender.mutation,
						err: runWebhookAdminCASMutationPostgres(
							ctx,
							contender.db,
							fixture,
							contender.mutation,
						),
					}
				}()
			}
			close(start)
			done := make(chan struct{})
			go func() {
				workers.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Webhook administrator CAS contenders deadlocked")
			}
			close(results)
			var winner webhookAdminCASMutation
			successes := 0
			conflicts := 0
			for result := range results {
				switch {
				case result.err == nil:
					successes++
					winner = result.mutation
				case errors.Is(result.err, ErrVersionConflict):
					var conflict *WebhookAdminVersionConflictError
					if !errors.As(result.err, &conflict) ||
						conflict.Current != 2 ||
						conflict.Expected != 1 {
						t.Fatalf(
							"%s conflict = %#v",
							result.mutation,
							result.err,
						)
					}
					conflicts++
				default:
					t.Fatalf(
						"%s contender error = %v",
						result.mutation,
						result.err,
					)
				}
			}
			if successes != 1 || conflicts != 1 {
				t.Fatalf(
					"CAS outcomes successes=%d conflicts=%d winner=%s",
					successes,
					conflicts,
					winner,
				)
			}
			assertWebhookAdminCASPostgresOutcome(
				t,
				fixture,
				pair,
				winner,
			)
		})
	}
}

type webhookAdminCASMutation string

const (
	webhookAdminCASUpdate          webhookAdminCASMutation = "update"
	webhookAdminCASDelete          webhookAdminCASMutation = "delete"
	webhookAdminCASEmergencyRevoke webhookAdminCASMutation = "emergency_revoke"
)

func prepareWebhookAdminCASPostgresFixture(
	t *testing.T,
	fixture *webhookOutboxLifecyclePostgresFixture,
) {
	t.Helper()
	tableOnly := fixture.adminScoped.Session(&gorm.Session{NewDB: true})
	tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
	if err := tableOnly.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	role := quoteWebhookPostgresIdentifier(fixture.runtimeRole)
	for _, statement := range []string{
		"GRANT SELECT (id, key, version) ON system_configs TO " + role,
		"GRANT UPDATE (version, updated_at, updated_by) " +
			"ON system_configs TO " + role,
		"GRANT UPDATE (description, deleted_at) " +
			"ON webhook_configs TO " + role,
	} {
		if err := fixture.adminScoped.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	subject := WebhookAdminSubject(
		postgresLifecycleConfigAActiveID,
	)
	anchor := models.SystemConfig{
		Key: AdminResourceVersionKey(
			fixture.projectA.Scope(),
			subject,
		),
		Value:        subject,
		ValueType:    "string",
		Description:  "Administrator command resource version",
		Category:     "security",
		Group:        "agent-resource-version",
		IsActive:     true,
		DefaultValue: subject,
		Version:      1,
	}
	if err := fixture.adminScoped.Create(&anchor).Error; err != nil {
		t.Fatal(err)
	}
	var privileges struct {
		IDSelect       bool `gorm:"column:id_select"`
		KeySelect      bool `gorm:"column:key_select"`
		VersionSelect  bool `gorm:"column:version_select"`
		VersionUpdate  bool `gorm:"column:version_update"`
		UpdatedAtWrite bool `gorm:"column:updated_at_write"`
		UpdatedByWrite bool `gorm:"column:updated_by_write"`
		InsertTable    bool `gorm:"column:insert_table"`
		DeleteTable    bool `gorm:"column:delete_table"`
	}
	if err := fixture.runtimeA.Raw(`
		SELECT
			has_column_privilege(
				current_user, 'system_configs', 'id', 'SELECT'
			) AS id_select,
			has_column_privilege(
				current_user, 'system_configs', 'key', 'SELECT'
			) AS key_select,
			has_column_privilege(
				current_user, 'system_configs', 'version', 'SELECT'
			) AS version_select,
			has_column_privilege(
				current_user, 'system_configs', 'version', 'UPDATE'
			) AS version_update,
			has_column_privilege(
				current_user, 'system_configs', 'updated_at', 'UPDATE'
			) AS updated_at_write,
			has_column_privilege(
				current_user, 'system_configs', 'updated_by', 'UPDATE'
			) AS updated_by_write,
			has_table_privilege(
				current_user, 'system_configs', 'INSERT'
			) AS insert_table,
			has_table_privilege(
				current_user, 'system_configs', 'DELETE'
			) AS delete_table
	`).Scan(&privileges).Error; err != nil {
		t.Fatal(err)
	}
	if !privileges.IDSelect ||
		!privileges.KeySelect ||
		!privileges.VersionSelect ||
		!privileges.VersionUpdate ||
		!privileges.UpdatedAtWrite ||
		!privileges.UpdatedByWrite ||
		privileges.InsertTable ||
		privileges.DeleteTable {
		t.Fatalf(
			"Webhook CAS runtime anchor ACL is not least privilege: %+v",
			privileges,
		)
	}
}

func runWebhookAdminCASMutationPostgres(
	ctx context.Context,
	db *gorm.DB,
	fixture *webhookOutboxLifecyclePostgresFixture,
	mutation webhookAdminCASMutation,
) error {
	return scopeddb.WithProjectScopeContextTransaction(
		ctx,
		db,
		fixture.projectA.Scope(),
		func(txCtx context.Context) error {
			return scopeddb.TransactionForContext(
				txCtx,
				db,
				func(tx *gorm.DB) error {
					if _, err :=
						CompareAndSwapWebhookAdminResourceVersionTx(
							txCtx,
							tx,
							fixture.projectA.Scope(),
							postgresLifecycleConfigAActiveID,
							1,
							postgresLifecycleEmergencyUserID,
						); err != nil {
						return err
					}
					config, err := lockWebhookConfigByID(
						tx.WithContext(txCtx),
						fixture.projectA.Scope(),
						postgresLifecycleConfigAActiveID,
						"UPDATE",
					)
					if err != nil {
						return err
					}
					switch mutation {
					case webhookAdminCASUpdate:
						if err := EnsureWebhookConfigMutableTx(
							txCtx,
							tx,
							fixture.projectA.Scope(),
							config.ID,
						); err != nil {
							return err
						}
						result := tx.WithContext(txCtx).
							Model(&models.WebhookConfig{}).
							Where(
								"id = ? AND organization_id = ? AND project_id = ?",
								config.ID,
								fixture.projectA.OrganizationID,
								fixture.projectA.ID,
							).
							Update(
								"description",
								"PostgreSQL CAS winner",
							)
						if result.Error != nil {
							return result.Error
						}
						if result.RowsAffected != 1 {
							return gorm.ErrRecordNotFound
						}
						return nil
					case webhookAdminCASDelete:
						result := tx.WithContext(txCtx).
							Where(
								"id = ? AND organization_id = ? AND project_id = ?",
								config.ID,
								fixture.projectA.OrganizationID,
								fixture.projectA.ID,
							).
							Delete(&models.WebhookConfig{})
						if result.Error != nil {
							return result.Error
						}
						if result.RowsAffected != 1 {
							return gorm.ErrRecordNotFound
						}
						return nil
					case webhookAdminCASEmergencyRevoke:
						_, err := fixture.service(
							db,
							fixture.now,
						).EmergencyRevokeWebhook(
							txCtx,
							config.ID,
						)
						return err
					default:
						return errors.New(
							"unknown Webhook administrator CAS mutation",
						)
					}
				},
			)
		},
	)
}

func assertWebhookAdminCASPostgresOutcome(
	t *testing.T,
	fixture *webhookOutboxLifecyclePostgresFixture,
	pair postgresLifecyclePair,
	winner webhookAdminCASMutation,
) {
	t.Helper()
	var anchor models.SystemConfig
	if err := fixture.adminScoped.First(
		&anchor,
		"key = ?",
		AdminResourceVersionKey(
			fixture.projectA.Scope(),
			WebhookAdminSubject(
				postgresLifecycleConfigAActiveID,
			),
		),
	).Error; err != nil {
		t.Fatal(err)
	}
	if anchor.Version != 2 {
		t.Fatalf("Webhook CAS anchor version = %d, want 2", anchor.Version)
	}
	var config models.WebhookConfig
	if err := fixture.adminScoped.Unscoped().First(
		&config,
		postgresLifecycleConfigAActiveID,
	).Error; err != nil {
		t.Fatal(err)
	}
	delivery := fixture.loadDelivery(t, pair.delivery.ID)
	snapshot := fixture.loadSnapshot(t, pair.snapshot.ID)
	credentialsLive := config.Secret != "" &&
		config.PreviousSecret != "" &&
		config.PreviousSecretExpiresAt != nil &&
		config.AccessToken != "" &&
		snapshot.Secret != "" &&
		snapshot.PreviousSecret != "" &&
		snapshot.PreviousSecretExpiresAt != nil &&
		snapshot.AccessToken != ""
	switch winner {
	case webhookAdminCASUpdate:
		if config.DeletedAt.Valid ||
			config.Status != models.WebhookStatusActive ||
			config.Description != "PostgreSQL CAS winner" ||
			!credentialsLive ||
			delivery.Status != models.OutboxDeliveryPending ||
			snapshot.CredentialShreddedAt != nil {
			t.Fatalf(
				"update winner leaked loser writes config=%+v delivery=%+v snapshot=%+v",
				config,
				delivery,
				snapshot,
			)
		}
	case webhookAdminCASDelete:
		if !config.DeletedAt.Valid ||
			config.Status != models.WebhookStatusActive ||
			config.Description != "" ||
			!credentialsLive ||
			delivery.Status != models.OutboxDeliveryPending ||
			snapshot.CredentialShreddedAt != nil {
			t.Fatalf(
				"delete winner leaked loser writes config=%+v delivery=%+v snapshot=%+v",
				config,
				delivery,
				snapshot,
			)
		}
	case webhookAdminCASEmergencyRevoke:
		if config.DeletedAt.Valid ||
			config.Status != models.WebhookStatusDisabled ||
			config.Description != "" ||
			config.Secret != "" ||
			config.PreviousSecret != "" ||
			config.PreviousSecretExpiresAt != nil ||
			config.AccessToken != "" ||
			delivery.Status != models.OutboxDeliveryExpired ||
			snapshot.CredentialShreddedAt == nil ||
			snapshot.Secret != "" ||
			snapshot.PreviousSecret != "" ||
			snapshot.PreviousSecretExpiresAt != nil ||
			snapshot.AccessToken != "" {
			t.Fatalf(
				"revoke winner leaked loser writes config=%+v delivery=%+v snapshot=%+v",
				config,
				delivery,
				snapshot,
			)
		}
	default:
		t.Fatalf("unknown Webhook CAS winner %q", winner)
	}
	var emergencyEvents int64
	if err := fixture.adminScoped.Model(&models.DomainEvent{}).
		Where(
			"type = ? AND subject = ?",
			WebhookEmergencyRevokedAdminEventType,
			WebhookAdminSubject(config.ID),
		).
		Count(&emergencyEvents).Error; err != nil {
		t.Fatal(err)
	}
	if emergencyEvents != 0 {
		t.Fatalf(
			"domain-only CAS race unexpectedly wrote %d administrator events",
			emergencyEvents,
		)
	}
}

package database

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresConcurrentAttachmentDownloadsSerializeFinalCountUpdate(
	t *testing.T,
) {
	for _, actorKind := range postgresAttachmentDownloadActorKinds() {
		t.Run(string(actorKind), func(t *testing.T) {
			fixture := openPostgresAttachmentDownloadFixture(
				t,
				"adc_"+string(actorKind[:1]),
				actorKind,
			)
			storage := newPostgresAttachmentDownloadBarrierStorage(
				fixture.attachment.StoragePath,
				fixture.content,
				2,
			)
			t.Cleanup(storage.release)
			service := services.NewAgentNativeService(
				fixture.runtimeDB,
				services.AgentNativeOptions{
					AttachmentStorage: storage,
				},
			)
			updateBarrier := registerPostgresAttachmentFinalUpdateBarrier(
				t,
				fixture.runtimeDB,
			)
			t.Cleanup(updateBarrier.release)

			operationContext, cancel := context.WithTimeout(
				fixture.operationContext(t),
				10*time.Second,
			)
			defer cancel()
			results := make(chan postgresAttachmentDownloadResult, 2)
			for range 2 {
				go runPostgresAttachmentDownload(
					service,
					operationContext,
					fixture.ticket.ID,
					fixture.attachment.ID,
					results,
				)
			}

			// Storage.Open is outside both project transactions. Reaching this
			// barrier proves both callers completed their initial authorized
			// reads before either is allowed to enter the final transaction.
			storage.waitForArrivals(t)
			storage.release()
			select {
			case <-updateBarrier.firstFinalLocked:
			case <-time.After(5 * time.Second):
				t.Fatal(
					"first attachment final transaction did not reach its count update",
				)
			}
			waitForPostgresAttachmentRuntimeLock(
				t,
				fixture.observerDB,
				fixture.runtimeRole,
			)
			updateBarrier.release()

			downloadCounts := make([]int, 0, 2)
			for range 2 {
				select {
				case result := <-results:
					if result.err != nil {
						t.Fatalf(
							"concurrent attachment download failed: %v",
							result.err,
						)
					}
					if result.attachment == nil {
						t.Fatal(
							"concurrent attachment download returned no metadata",
						)
					}
					if !bytes.Equal(result.content, fixture.content) {
						t.Fatalf(
							"downloaded attachment bytes = %q, want %q",
							result.content,
							fixture.content,
						)
					}
					downloadCounts = append(
						downloadCounts,
						result.attachment.DownloadCount,
					)
				case <-time.After(5 * time.Second):
					t.Fatal(
						"concurrent attachment downloads timed out after releasing the final lock",
					)
				}
			}
			sort.Ints(downloadCounts)
			if len(downloadCounts) != 2 ||
				downloadCounts[0] != 1 ||
				downloadCounts[1] != 2 {
				t.Fatalf(
					"serialized download results = %v, want [1 2]",
					downloadCounts,
				)
			}
			for range 2 {
				select {
				case rowsAffected := <-updateBarrier.rowsAffected:
					if rowsAffected != 1 {
						t.Fatalf(
							"attachment count update RowsAffected = %d, want 1",
							rowsAffected,
						)
					}
				case <-time.After(5 * time.Second):
					t.Fatal(
						"attachment count update did not report RowsAffected",
					)
				}
			}
			if count := readPostgresAttachmentDownloadCount(
				t,
				fixture.runtimeDB,
				fixture.project.Scope(),
				fixture.attachment.ID,
			); count != fixture.attachment.DownloadCount+2 {
				t.Fatalf(
					"durable attachment download_count = %d, want %d",
					count,
					fixture.attachment.DownloadCount+2,
				)
			}
		})
	}
}

func TestPostgresAttachmentDownloadFinalRevalidationFailsClosed(
	t *testing.T,
) {
	for _, actorKind := range postgresAttachmentDownloadActorKinds() {
		t.Run(string(actorKind), func(t *testing.T) {
			fixture := openPostgresAttachmentDownloadFixture(
				t,
				"adr_"+string(actorKind[:1]),
				actorKind,
			)

			t.Run("authorization_revoked", func(t *testing.T) {
				storage := newPostgresAttachmentDownloadBarrierStorage(
					fixture.attachment.StoragePath,
					fixture.content,
					1,
				)
				t.Cleanup(storage.release)
				service := services.NewAgentNativeService(
					fixture.runtimeDB,
					services.AgentNativeOptions{
						AttachmentStorage: storage,
					},
				)
				operationContext, cancel := context.WithTimeout(
					fixture.operationContext(t),
					10*time.Second,
				)
				defer cancel()
				result := make(
					chan postgresAttachmentDownloadResult,
					1,
				)
				go runPostgresAttachmentDownload(
					service,
					operationContext,
					fixture.ticket.ID,
					fixture.attachment.ID,
					result,
				)
				storage.waitForArrivals(t)
				fixture.setAuthorizationActive(t, false)
				storage.release()
				assertPostgresAttachmentDownloadDenied(
					t,
					result,
					"revoked authorization",
				)
				if count := readPostgresAttachmentDownloadCount(
					t,
					fixture.runtimeDB,
					fixture.project.Scope(),
					fixture.attachment.ID,
				); count != fixture.attachment.DownloadCount {
					t.Fatalf(
						"revoked download_count = %d, want %d",
						count,
						fixture.attachment.DownloadCount,
					)
				}
				fixture.setAuthorizationActive(t, true)
			})

			t.Run("protected_metadata_changed", func(t *testing.T) {
				storage := newPostgresAttachmentDownloadBarrierStorage(
					fixture.attachment.StoragePath,
					fixture.content,
					1,
				)
				t.Cleanup(storage.release)
				service := services.NewAgentNativeService(
					fixture.runtimeDB,
					services.AgentNativeOptions{
						AttachmentStorage: storage,
					},
				)
				operationContext, cancel := context.WithTimeout(
					fixture.operationContext(t),
					10*time.Second,
				)
				defer cancel()
				result := make(
					chan postgresAttachmentDownloadResult,
					1,
				)
				go runPostgresAttachmentDownload(
					service,
					operationContext,
					fixture.ticket.ID,
					fixture.attachment.ID,
					result,
				)
				storage.waitForArrivals(t)
				mutatedPath := fixture.attachment.StoragePath +
					"-mutated"
				err := WithProjectScopeTransaction(
					context.Background(),
					fixture.ownerDB,
					fixture.project.Scope(),
					func(scoped *gorm.DB) error {
						update := scoped.
							Model(&models.TicketAttachment{}).
							Where(
								"id = ?",
								fixture.attachment.ID,
							).
							Update("storage_path", mutatedPath)
						if update.Error != nil {
							return update.Error
						}
						if update.RowsAffected != 1 {
							return fmt.Errorf(
								"metadata mutation affected %d rows",
								update.RowsAffected,
							)
						}
						return nil
					},
				)
				if err != nil {
					t.Fatalf(
						"mutate protected attachment metadata: %v",
						err,
					)
				}
				storage.release()
				assertPostgresAttachmentDownloadDenied(
					t,
					result,
					"changed protected metadata",
				)
				if count := readPostgresAttachmentDownloadCount(
					t,
					fixture.runtimeDB,
					fixture.project.Scope(),
					fixture.attachment.ID,
				); count != fixture.attachment.DownloadCount {
					t.Fatalf(
						"metadata-raced download_count = %d, want %d",
						count,
						fixture.attachment.DownloadCount,
					)
				}
			})
		})
	}
}

func TestPostgresRequesterCommentAttachmentUploadBoundary(
	t *testing.T,
) {
	fixture := openPostgresAttachmentDownloadFixture(
		t,
		"aru_h",
		postgresAttachmentDownloadHuman,
	)
	if fixture.actorKind != postgresAttachmentDownloadHuman {
		t.Fatal("requester attachment fixture is not Human")
	}
	if err := WithProjectScopeTransaction(
		context.Background(),
		fixture.ownerDB,
		fixture.project.Scope(),
		func(scoped *gorm.DB) error {
			membership := scoped.Model(
				&models.ProjectMembership{},
			).Where(
				"id = ? AND project_id = ? AND user_id = ?",
				fixture.membership.ID,
				fixture.project.ID,
				fixture.user.ID,
			).Updates(map[string]any{
				"role":      models.ProjectRoleRequester,
				"is_active": true,
			})
			if membership.Error != nil {
				return membership.Error
			}
			if membership.RowsAffected != 1 {
				return fmt.Errorf(
					"requester membership update affected %d rows",
					membership.RowsAffected,
				)
			}

			otherTicket := fixture.ticket
			otherTicket.ID = 0
			otherTicket.PublicID = ""
			otherTicket.CreatedAt = time.Time{}
			otherTicket.UpdatedAt = time.Time{}
			otherTicket.DeletedAt = gorm.DeletedAt{}
			otherTicket.TicketNumber += "-OTHER"
			otherTicket.Title = "Requester cross-ticket comment boundary"
			otherTicket.Version = 1
			otherTicket.Comments = nil
			otherTicket.History = nil
			if err := scoped.Create(&otherTicket).Error; err != nil {
				return err
			}
			comments := []models.TicketComment{
				{
					TicketID:  fixture.ticket.ID,
					UserID:    &fixture.user.ID,
					ActorType: models.ActorTypeHuman,
					ActorID: models.HumanActor(
						fixture.user.ID,
					).ID,
					Content: "requester public comment",
					Type:    models.CommentTypePublic,
				},
				{
					TicketID:  fixture.ticket.ID,
					UserID:    &fixture.user.ID,
					ActorType: models.ActorTypeHuman,
					ActorID: models.HumanActor(
						fixture.user.ID,
					).ID,
					Content: "requester internal comment",
					Type:    models.CommentTypeInternal,
				},
				{
					TicketID:  otherTicket.ID,
					UserID:    &fixture.user.ID,
					ActorType: models.ActorTypeHuman,
					ActorID: models.HumanActor(
						fixture.user.ID,
					).ID,
					Content: "requester other-ticket comment",
					Type:    models.CommentTypePublic,
				},
				{
					TicketID:  fixture.ticket.ID,
					UserID:    &fixture.user.ID,
					ActorType: models.ActorTypeHuman,
					ActorID: models.HumanActor(
						fixture.user.ID,
					).ID,
					Content:   "requester deleted comment",
					Type:      models.CommentTypePublic,
					IsDeleted: true,
				},
			}
			if err := scoped.Create(&comments).Error; err != nil {
				return err
			}
			return nil
		},
	); err != nil {
		t.Fatalf("seed requester comment upload matrix: %v", err)
	}

	var comments []models.TicketComment
	if err := WithProjectScopeTransaction(
		context.Background(),
		fixture.ownerDB,
		fixture.project.Scope(),
		func(scoped *gorm.DB) error {
			return scoped.Where(
				"user_id = ? AND content LIKE ?",
				fixture.user.ID,
				"requester %",
			).Order("id ASC").Find(&comments).Error
		},
	); err != nil {
		t.Fatalf("reload requester comment upload matrix: %v", err)
	}
	if len(comments) != 4 {
		t.Fatalf(
			"requester comment upload fixtures = %d, want 4",
			len(comments),
		)
	}
	commentsByContent := make(
		map[string]models.TicketComment,
		len(comments),
	)
	for _, comment := range comments {
		commentsByContent[comment.Content] = comment
	}

	storage, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := services.NewAgentNativeService(
		fixture.runtimeDB,
		services.AgentNativeOptions{
			AttachmentStorage:  storage,
			AttachmentStaging:  storage,
			AttachmentMaxBytes: 1024,
		},
	)
	operationContext := fixture.operationContext(t)
	deniedCommentIDs := make([]uint, 0, 3)
	for _, testCase := range []struct {
		name    string
		comment models.TicketComment
	}{
		{
			name:    "internal_comment_403",
			comment: commentsByContent["requester internal comment"],
		},
		{
			name:    "cross_ticket_comment_403",
			comment: commentsByContent["requester other-ticket comment"],
		},
		{
			name:    "deleted_comment_403",
			comment: commentsByContent["requester deleted comment"],
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			commentID := testCase.comment.ID
			deniedCommentIDs = append(
				deniedCommentIDs,
				commentID,
			)
			_, storeErr := service.StoreAttachment(
				operationContext,
				services.NativeAttachmentInput{
					TicketID:  fixture.ticket.ID,
					CommentID: &commentID,
					ExpectedVersion: fixture.ticket.
						Version,
					Actor: models.HumanActor(
						fixture.user.ID,
					),
					SourceProtocol: "rest-human",
					OriginalName:   testCase.name + ".txt",
					ContentType:    "text/plain",
					IsPublic:       true,
					Reader: bytes.NewBufferString(
						"requester denied attachment",
					),
				},
			)
			assertPostgresRequesterAttachmentDenied(
				t,
				storeErr,
			)
		})
	}
	var deniedAttachmentRows int64
	if err := WithProjectScopeTransaction(
		context.Background(),
		fixture.runtimeDB,
		fixture.project.Scope(),
		func(scoped *gorm.DB) error {
			return scoped.Model(&models.TicketAttachment{}).
				Where("comment_id IN ?", deniedCommentIDs).
				Count(&deniedAttachmentRows).Error
		},
	); err != nil {
		t.Fatalf(
			"count denied requester attachments under FORCE RLS: %v",
			err,
		)
	}
	if deniedAttachmentRows != 0 {
		t.Fatalf(
			"denied requester comments persisted %d attachment intents",
			deniedAttachmentRows,
		)
	}

	t.Run("own_public_comment_202", func(t *testing.T) {
		publicComment := commentsByContent["requester public comment"]
		commentID := publicComment.ID
		result, storeErr := service.StoreAttachment(
			operationContext,
			services.NativeAttachmentInput{
				TicketID:        fixture.ticket.ID,
				CommentID:       &commentID,
				ExpectedVersion: fixture.ticket.Version,
				Actor: models.HumanActor(
					fixture.user.ID,
				),
				SourceProtocol: "rest-human",
				OriginalName:   "requester-public.txt",
				ContentType:    "text/plain",
				IsPublic:       true,
				Reader: bytes.NewBufferString(
					"requester accepted attachment",
				),
			},
		)
		if storeErr != nil {
			t.Fatalf(
				"requester public-comment upload status=500 err=%v",
				storeErr,
			)
		}
		if result == nil ||
			result.Attachment == nil ||
			result.Event == nil ||
			result.Receipt.ResourceVersion !=
				fixture.ticket.Version+1 ||
			result.Attachment.CommentID == nil ||
			*result.Attachment.CommentID != publicComment.ID ||
			!result.Attachment.IsPublic {
			t.Fatalf(
				"requester public-comment upload status=202 returned inconsistent result: %+v",
				result,
			)
		}
		var acceptedRows int64
		if err := WithProjectScopeTransaction(
			context.Background(),
			fixture.runtimeDB,
			fixture.project.Scope(),
			func(scoped *gorm.DB) error {
				return scoped.Model(
					&models.TicketAttachment{},
				).Where(
					"id = ? AND comment_id = ? AND storage_type = ?",
					result.Attachment.ID,
					publicComment.ID,
					"staging",
				).Count(&acceptedRows).Error
			},
		); err != nil {
			t.Fatalf(
				"read accepted requester attachment under FORCE RLS: %v",
				err,
			)
		}
		if acceptedRows != 1 {
			t.Fatalf(
				"requester public-comment status=202 durable rows=%d, want 1",
				acceptedRows,
			)
		}
		workerContext, err := services.WithOperationContext(
			context.Background(),
			services.OperationContext{
				Scope: fixture.project.Scope(),
				Actor: models.SystemActor(
					"outbox-delivery-worker",
				),
				Source: services.SourceProtocolWorker,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.ExecuteAttachmentUploadOutbox(
			workerContext,
			result.Attachment.ID,
		); err != nil {
			t.Fatalf(
				"finalize accepted requester attachment under FORCE RLS: %v",
				err,
			)
		}
		var finalized models.TicketAttachment
		if err := WithProjectScopeTransaction(
			context.Background(),
			fixture.runtimeDB,
			fixture.project.Scope(),
			func(scoped *gorm.DB) error {
				return scoped.Where(
					"id = ?",
					result.Attachment.ID,
				).Take(&finalized).Error
			},
		); err != nil {
			t.Fatalf(
				"load finalized requester attachment under FORCE RLS: %v",
				err,
			)
		}
		if finalized.StorageType != "local" ||
			!strings.HasPrefix(
				finalized.StoragePath,
				fmt.Sprintf(
					"tickets/%d/",
					fixture.ticket.ID,
				),
			) {
			t.Fatalf(
				"finalized requester attachment storage is inconsistent: type=%q path=%q",
				finalized.StorageType,
				finalized.StoragePath,
			)
		}
	})
}

type postgresAttachmentDownloadActorKind string

const (
	postgresAttachmentDownloadHuman   postgresAttachmentDownloadActorKind = "human"
	postgresAttachmentDownloadMachine postgresAttachmentDownloadActorKind = "machine"
)

func postgresAttachmentDownloadActorKinds() []postgresAttachmentDownloadActorKind {
	return []postgresAttachmentDownloadActorKind{
		postgresAttachmentDownloadHuman,
		postgresAttachmentDownloadMachine,
	}
}

type postgresAttachmentDownloadFixture struct {
	ownerDB     *gorm.DB
	observerDB  *gorm.DB
	runtimeDB   *gorm.DB
	runtimeRole string
	project     models.Project
	user        models.User
	membership  models.ProjectMembership
	machine     *postgresMachineAuthorizationFixture
	actorKind   postgresAttachmentDownloadActorKind
	ticket      models.Ticket
	attachment  models.TicketAttachment
	content     []byte
}

func openPostgresAttachmentDownloadFixture(
	t *testing.T,
	name string,
	actorKind postgresAttachmentDownloadActorKind,
) postgresAttachmentDownloadFixture {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL attachment download evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse PostgreSQL integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal(
				"attachment download integration test requires a loopback PostgreSQL target",
			)
		}
	}
	silentConfig := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	adminDB, err := gorm.Open(postgres.Open(rawDSN), silentConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL attachment administrator: %v", err)
	}
	adminSQL, err := adminDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	runtimeRole := ""
	roleCreated := false
	// This cleanup is deliberately registered before the shared schema
	// fixture. LIFO cleanup drops the schema (and its grants) before dropping
	// this non-owner runtime role.
	t.Cleanup(func() {
		if roleCreated {
			if cleanupErr := adminDB.Exec(
				"DROP ROLE IF EXISTS " +
					quotePostgresAttachmentIdentifier(runtimeRole),
			).Error; cleanupErr != nil {
				t.Errorf(
					"drop PostgreSQL attachment runtime role: %v",
					cleanupErr,
				)
			}
		}
		if closeErr := adminSQL.Close(); closeErr != nil {
			t.Errorf(
				"close PostgreSQL attachment administrator: %v",
				closeErr,
			)
		}
	})

	ownerDB, _, project, suffix :=
		openPostgresAuthorizationBarrierFixture(t, name)
	bootstrapPostgresTicketConfiguration(t, ownerDB)
	user, ticket, _ := seedPostgresTicketLeaseFixture(
		t,
		ownerDB,
		project,
		"download-"+string(actorKind)+"-"+suffix,
		time.Now().UTC().Add(10*time.Minute),
	)
	var membership models.ProjectMembership
	if err := ownerDB.Where(
		"project_id = ? AND user_id = ?",
		project.ID,
		user.ID,
	).Take(&membership).Error; err != nil {
		t.Fatalf("load attachment Human membership: %v", err)
	}

	var machine *postgresMachineAuthorizationFixture
	if actorKind == postgresAttachmentDownloadMachine {
		seeded := seedPostgresMachineAuthorizationFixture(
			t,
			ownerDB,
			project,
			"attachment-download-"+suffix,
		)
		readScopes := datatypes.JSON(`["attachments:read"]`)
		if err := ownerDB.Model(&models.ServicePrincipal{}).
			Where("id = ?", seeded.principal.ID).
			Update("scopes", readScopes).Error; err != nil {
			t.Fatalf("grant attachment read Principal scope: %v", err)
		}
		if err := ownerDB.Model(&models.ProjectPrincipalGrant{}).
			Where("id = ?", seeded.grant.ID).
			Update("scopes", readScopes).Error; err != nil {
			t.Fatalf("grant attachment read project scope: %v", err)
		}
		seeded.principal.Scopes = readScopes
		seeded.grant.Scopes = readScopes
		machine = &seeded
	}

	content := []byte(
		"ChronoDesk PostgreSQL attachment download barrier " +
			string(actorKind),
	)
	contentHash := sha256.Sum256(content)
	scannedAt := time.Now().UTC().Truncate(time.Microsecond)
	attachment := models.TicketAttachment{
		OrganizationID: project.OrganizationID,
		ProjectID:      project.ID,
		TicketID:       ticket.ID,
		UploadedBy:     &user.ID,
		ActorType:      models.ActorTypeHuman,
		ActorID:        models.HumanActor(user.ID).ID,
		FileName:       "download-evidence.txt",
		OriginalName:   "download-evidence.txt",
		FileSize:       int64(len(content)),
		MimeType:       "text/plain",
		FileType:       models.AttachmentTypeDocument,
		Extension:      ".txt",
		StoragePath:    "attachment-download/" + suffix,
		StorageType:    "local",
		IsPublic:       false,
		DownloadCount:  0,
		Hash:           fmt.Sprintf("%x", contentHash[:]),
		VirusScan:      models.VirusScanClean,
		ScannedAt:      &scannedAt,
	}
	if err := WithProjectScopeTransaction(
		context.Background(),
		ownerDB,
		project.Scope(),
		func(scoped *gorm.DB) error {
			return scoped.Create(&attachment).Error
		},
	); err != nil {
		t.Fatalf("seed clean PostgreSQL attachment: %v", err)
	}
	if err := EnableProjectRLS(ownerDB); err != nil {
		t.Fatalf("enable attachment fixture FORCE RLS: %v", err)
	}

	var schemaName string
	if err := ownerDB.Raw("SELECT current_schema()").
		Scan(&schemaName).Error; err != nil {
		t.Fatalf("read attachment fixture schema: %v", err)
	}
	runtimeRole = "chronodesk_attachment_runtime_" + suffix
	runtimePassword := "ChronoDeskAttachment" + suffix + "!"
	quotedRole := quotePostgresAttachmentIdentifier(runtimeRole)
	quotedSchema := quotePostgresAttachmentIdentifier(schemaName)
	if err := adminDB.Exec(
		"CREATE ROLE " + quotedRole +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quotePostgresAttachmentLiteral(runtimePassword),
	).Error; err != nil {
		t.Fatalf("create PostgreSQL attachment runtime role: %v", err)
	}
	roleCreated = true
	for _, statement := range []string{
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRole,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " +
			quotedSchema + " TO " + quotedRole,
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA " +
			quotedSchema + " TO " + quotedRole,
	} {
		if err := ownerDB.Exec(statement).Error; err != nil {
			t.Fatalf(
				"grant PostgreSQL attachment runtime privilege: %v",
				err,
			)
		}
	}

	runtimeURL := *parsed
	runtimeURL.User = url.UserPassword(runtimeRole, runtimePassword)
	runtimeQuery := runtimeURL.Query()
	runtimeQuery.Set("search_path", schemaName)
	runtimeURL.RawQuery = runtimeQuery.Encode()
	runtimeDB, err := gorm.Open(
		postgres.Open(runtimeURL.String()),
		silentConfig,
	)
	if err != nil {
		t.Fatalf("open non-owner PostgreSQL attachment runtime: %v", err)
	}
	runtimeSQL, err := runtimeDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	runtimeSQL.SetMaxOpenConns(4)
	runtimeSQL.SetMaxIdleConns(4)
	t.Cleanup(func() {
		if closeErr := runtimeSQL.Close(); closeErr != nil {
			t.Errorf(
				"close PostgreSQL attachment runtime pool: %v",
				closeErr,
			)
		}
	})
	if err := ValidateProjectRLSRuntime(runtimeDB); err != nil {
		t.Fatalf("validate attachment FORCE RLS runtime: %v", err)
	}
	if err := ValidateProjectRuntimeRole(runtimeDB); err != nil {
		t.Fatalf("validate attachment non-owner runtime role: %v", err)
	}
	if err := InstallProjectScopeTransactionRouting(runtimeDB); err != nil {
		t.Fatalf("install attachment scoped transaction routing: %v", err)
	}
	var unscopedAttachments int64
	if err := runtimeDB.Model(&models.TicketAttachment{}).
		Count(&unscopedAttachments).Error; err != nil {
		t.Fatalf("query unscoped attachment rows: %v", err)
	}
	if unscopedAttachments != 0 {
		t.Fatalf(
			"FORCE RLS exposed %d unscoped attachment rows",
			unscopedAttachments,
		)
	}
	return postgresAttachmentDownloadFixture{
		ownerDB:     ownerDB,
		observerDB:  adminDB,
		runtimeDB:   runtimeDB,
		runtimeRole: runtimeRole,
		project:     project,
		user:        user,
		membership:  membership,
		machine:     machine,
		actorKind:   actorKind,
		ticket:      ticket,
		attachment:  attachment,
		content:     content,
	}
}

func (fixture postgresAttachmentDownloadFixture) operationContext(
	t *testing.T,
) context.Context {
	t.Helper()
	operation := services.OperationContext{
		Scope: fixture.project.Scope(),
	}
	switch fixture.actorKind {
	case postgresAttachmentDownloadHuman:
		operation.Actor = models.HumanActor(fixture.user.ID)
		operation.Source = services.SourceProtocolHumanREST
	case postgresAttachmentDownloadMachine:
		if fixture.machine == nil {
			t.Fatal("Machine attachment fixture is unavailable")
		}
		operation.Actor = models.ServicePrincipalActor(
			fixture.machine.principal.ID,
		)
		operation.Source = services.SourceProtocolAgentREST
		operation.CredentialID = fixture.machine.credential.ID
	default:
		t.Fatalf(
			"unsupported attachment actor fixture %q",
			fixture.actorKind,
		)
	}
	ctx, err := services.WithOperationContext(
		context.Background(),
		operation,
	)
	if err != nil {
		t.Fatalf("bind attachment download operation context: %v", err)
	}
	return ctx
}

func (fixture postgresAttachmentDownloadFixture) setAuthorizationActive(
	t *testing.T,
	active bool,
) {
	t.Helper()
	switch fixture.actorKind {
	case postgresAttachmentDownloadHuman:
		update := fixture.ownerDB.
			Model(&models.ProjectMembership{}).
			Where("id = ?", fixture.membership.ID).
			Update("is_active", active)
		if update.Error != nil {
			t.Fatalf(
				"set attachment Human membership active=%t: %v",
				active,
				update.Error,
			)
		}
		if update.RowsAffected != 1 {
			t.Fatalf(
				"set attachment Human membership active=%t affected %d rows",
				active,
				update.RowsAffected,
			)
		}
	case postgresAttachmentDownloadMachine:
		if fixture.machine == nil {
			t.Fatal("Machine attachment fixture is unavailable")
		}
		update := fixture.ownerDB.
			Model(&models.ProjectPrincipalGrant{}).
			Where("id = ?", fixture.machine.grant.ID).
			Update("is_active", active)
		if update.Error != nil {
			t.Fatalf(
				"set attachment Principal Grant active=%t: %v",
				active,
				update.Error,
			)
		}
		if update.RowsAffected != 1 {
			t.Fatalf(
				"set attachment Principal Grant active=%t affected %d rows",
				active,
				update.RowsAffected,
			)
		}
	default:
		t.Fatalf(
			"unsupported attachment actor fixture %q",
			fixture.actorKind,
		)
	}
}

type postgresAttachmentDownloadBarrierStorage struct {
	path        string
	content     []byte
	expected    int32
	opens       atomic.Int32
	arrivals    chan struct{}
	released    chan struct{}
	releaseOnce sync.Once
}

func newPostgresAttachmentDownloadBarrierStorage(
	path string,
	content []byte,
	expected int,
) *postgresAttachmentDownloadBarrierStorage {
	return &postgresAttachmentDownloadBarrierStorage{
		path:     path,
		content:  append([]byte(nil), content...),
		expected: int32(expected),
		arrivals: make(chan struct{}, expected),
		released: make(chan struct{}),
	}
}

func (storage *postgresAttachmentDownloadBarrierStorage) Put(
	context.Context,
	string,
	io.Reader,
	int64,
) (*services.StoredAttachmentObject, error) {
	return nil, errors.New(
		"attachment download evidence storage does not support Put",
	)
}

func (storage *postgresAttachmentDownloadBarrierStorage) Open(
	ctx context.Context,
	path string,
) (io.ReadCloser, error) {
	if path != storage.path {
		return nil, fmt.Errorf(
			"attachment storage path = %q, want %q",
			path,
			storage.path,
		)
	}
	call := storage.opens.Add(1)
	if call > storage.expected {
		return nil, fmt.Errorf(
			"attachment storage Open calls = %d, want at most %d",
			call,
			storage.expected,
		)
	}
	storage.arrivals <- struct{}{}
	select {
	case <-storage.released:
		return io.NopCloser(
			bytes.NewReader(append([]byte(nil), storage.content...)),
		), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (storage *postgresAttachmentDownloadBarrierStorage) Delete(
	context.Context,
	string,
) error {
	return errors.New(
		"attachment download evidence storage does not support Delete",
	)
}

func (storage *postgresAttachmentDownloadBarrierStorage) waitForArrivals(
	t *testing.T,
) {
	t.Helper()
	for index := int32(0); index < storage.expected; index++ {
		select {
		case <-storage.arrivals:
		case <-time.After(5 * time.Second):
			t.Fatalf(
				"attachment storage Open arrivals = %d, want %d",
				index,
				storage.expected,
			)
		}
	}
}

func (storage *postgresAttachmentDownloadBarrierStorage) release() {
	storage.releaseOnce.Do(func() {
		close(storage.released)
	})
}

type postgresAttachmentFinalUpdateBarrier struct {
	firstFinalLocked chan struct{}
	rowsAffected     chan int64
	released         chan struct{}
	releaseOnce      sync.Once
}

func registerPostgresAttachmentFinalUpdateBarrier(
	t *testing.T,
	db *gorm.DB,
) *postgresAttachmentFinalUpdateBarrier {
	t.Helper()
	const (
		beforeCallback = "test:attachment_download_final_lock"
		afterCallback  = "test:attachment_download_rows_affected"
	)
	barrier := &postgresAttachmentFinalUpdateBarrier{
		firstFinalLocked: make(chan struct{}),
		rowsAffected:     make(chan int64, 2),
		released:         make(chan struct{}),
	}
	var first atomic.Bool
	if err := db.Callback().Update().
		Before("gorm:update").
		Register(
			beforeCallback,
			func(callbackTx *gorm.DB) {
				if callbackTx.Statement.Table !=
					(models.TicketAttachment{}).TableName() ||
					!first.CompareAndSwap(false, true) {
					return
				}
				close(barrier.firstFinalLocked)
				select {
				case <-barrier.released:
				case <-callbackTx.Statement.Context.Done():
					_ = callbackTx.AddError(
						callbackTx.Statement.Context.Err(),
					)
				}
			},
		); err != nil {
		t.Fatalf(
			"register attachment final lock callback: %v",
			err,
		)
	}
	if err := db.Callback().Update().
		After("gorm:update").
		Register(
			afterCallback,
			func(callbackTx *gorm.DB) {
				if callbackTx.Statement.Table ==
					(models.TicketAttachment{}).TableName() {
					barrier.rowsAffected <- callbackTx.RowsAffected
				}
			},
		); err != nil {
		_ = db.Callback().Update().Remove(beforeCallback)
		t.Fatalf(
			"register attachment RowsAffected callback: %v",
			err,
		)
	}
	t.Cleanup(func() {
		_ = db.Callback().Update().Remove(beforeCallback)
		_ = db.Callback().Update().Remove(afterCallback)
	})
	return barrier
}

func (barrier *postgresAttachmentFinalUpdateBarrier) release() {
	barrier.releaseOnce.Do(func() {
		close(barrier.released)
	})
}

type postgresAttachmentDownloadResult struct {
	attachment *models.TicketAttachment
	content    []byte
	err        error
}

func runPostgresAttachmentDownload(
	service *services.AgentNativeService,
	ctx context.Context,
	ticketID uint,
	attachmentID uint,
	results chan<- postgresAttachmentDownloadResult,
) {
	attachment, reader, err := service.OpenTicketAttachment(
		ctx,
		ticketID,
		attachmentID,
	)
	if err != nil {
		results <- postgresAttachmentDownloadResult{err: err}
		return
	}
	content, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		results <- postgresAttachmentDownloadResult{err: readErr}
		return
	}
	if closeErr != nil {
		results <- postgresAttachmentDownloadResult{err: closeErr}
		return
	}
	results <- postgresAttachmentDownloadResult{
		attachment: attachment,
		content:    content,
	}
}

func waitForPostgresAttachmentRuntimeLock(
	t *testing.T,
	observer *gorm.DB,
	runtimeRole string,
) {
	t.Helper()
	deadline := time.Now().Add(postgresAuthorizationBarrierTimeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int64
		if err := observer.Raw(
			`SELECT COUNT(*)
			 FROM pg_stat_activity
			 WHERE usename = ?
			   AND wait_event_type = 'Lock'`,
			runtimeRole,
		).Scan(&waiting).Error; err != nil {
			t.Fatalf(
				"inspect attachment runtime lock wait: %v",
				err,
			)
		}
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"attachment runtime %q did not reach a PostgreSQL lock wait",
				runtimeRole,
			)
		}
		<-ticker.C
	}
}

func readPostgresAttachmentDownloadCount(
	t *testing.T,
	db *gorm.DB,
	scope models.ProjectScope,
	attachmentID uint,
) int {
	t.Helper()
	var attachment models.TicketAttachment
	if err := WithProjectScopeTransaction(
		context.Background(),
		db,
		scope,
		func(scoped *gorm.DB) error {
			return scoped.Select("id", "download_count").
				Where("id = ?", attachmentID).
				Take(&attachment).Error
		},
	); err != nil {
		t.Fatalf("read durable attachment download_count: %v", err)
	}
	return attachment.DownloadCount
}

func assertPostgresAttachmentDownloadDenied(
	t *testing.T,
	results <-chan postgresAttachmentDownloadResult,
	reason string,
) {
	t.Helper()
	select {
	case result := <-results:
		if !errors.Is(result.err, services.ErrProjectAccessDenied) {
			t.Fatalf(
				"%s download error = %v, want ErrProjectAccessDenied",
				reason,
				result.err,
			)
		}
		if result.attachment != nil || result.content != nil {
			t.Fatalf(
				"%s returned attachment content: %+v",
				reason,
				result,
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s download timed out", reason)
	}
}

func assertPostgresRequesterAttachmentDenied(
	t *testing.T,
	err error,
) {
	t.Helper()
	if !errors.Is(err, services.ErrProjectAccessDenied) {
		t.Fatalf(
			"requester comment attachment status=%d error=%v, want ErrProjectAccessDenied",
			500,
			err,
		)
	}
	lower := strings.ToLower(err.Error())
	for _, databaseError := range []string{
		"row-level security",
		"violates row level security",
		"permission denied for",
		"insufficient privilege",
	} {
		if strings.Contains(lower, databaseError) {
			t.Fatalf(
				"requester comment attachment exposed base-RLS error %q: %v",
				databaseError,
				err,
			)
		}
	}
}

func quotePostgresAttachmentIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quotePostgresAttachmentLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

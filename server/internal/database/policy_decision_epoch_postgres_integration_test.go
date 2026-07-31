package database

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestPostgresPreparedPolicyDecisionRejectsPolicySetEpochChange(
	t *testing.T,
) {
	for _, testCase := range postgresPolicyEpochProtocolCases() {
		t.Run(testCase.name, func(t *testing.T) {
			db, _, project, suffix :=
				openPostgresAuthorizationBarrierFixture(
					t,
					"pe_stale_"+testCase.fixture,
				)
			bootstrapPostgresTicketConfiguration(t, db)
			fixture := seedPostgresPolicyEpochCommentFixture(
				t,
				db,
				project,
				"stale-"+testCase.fixture+"-"+suffix,
			)
			_, commandDatabases, _ :=
				openPostgresAuthorizationCommandDatabases(t, db, 2)
			commandService := newPostgresPolicyEpochService(
				t,
				commandDatabases[0],
			)
			policyService := newPostgresPolicyEpochService(
				t,
				commandDatabases[1],
			)
			operationContext := fixture.operationContext(
				t,
				testCase.source,
			)
			reservation, err := commandService.ReserveIdempotency(
				operationContext,
				fixture.actor,
				string(services.NativeCommandCommentCreate),
				"stale-"+testCase.fixture,
				[]byte(`{"content":"must not commit"}`),
				time.Hour,
			)
			if err != nil {
				t.Fatalf("reserve stale command idempotency: %v", err)
			}

			requestDigest := "policy-epoch-stale-" + testCase.fixture
			// T1 completes the short, auditable pre-authorization transaction
			// and pauses before opening the final business transaction.
			authorizedContext, err :=
				commandService.
					AuthorizeNativeCommandInShortProjectTransactions(
						operationContext,
						fixture.authorizationInput(
							testCase.source,
							requestDigest,
						),
					)
			if err != nil {
				t.Fatalf("prepare comment command authorization: %v", err)
			}
			preparedDecision := loadPostgresPolicyEpochDecision(
				t,
				db,
				fixture,
				testCase.source,
				requestDigest,
			)
			if !preparedDecision.Allowed ||
				preparedDecision.PolicyEpoch !=
					fixture.initialPolicyEpoch {
				t.Fatalf(
					"prepared decision = %+v, want allowed epoch %d",
					preparedDecision,
					fixture.initialPolicyEpoch,
				)
			}
			decisionsBefore := countPostgresPolicyEpochDecisions(
				t,
				db,
				fixture,
			)
			businessBefore := readPostgresPolicyEpochBusinessState(
				t,
				db,
				fixture,
				reservation.Record.ID,
			)

			// T2 uses the production policy mutation path. Its commit both
			// creates the deny and advances the serialized policy-set epoch.
			denyPolicy, err := policyService.CreateAgentPolicy(
				operationContext,
				fixture.denyPolicyInput("deny stale prepared comment"),
			)
			if err != nil {
				t.Fatalf("commit intervening deny policy: %v", err)
			}
			assertPostgresPolicyEpoch(
				t,
				db,
				fixture.machine.principal.ID,
				fixture.initialPolicyEpoch+1,
			)

			// T1 now opens its final scoped transaction. Revalidation may lock
			// the current principal, but the exact prepared decision still
			// belongs to the preceding policy-set epoch and must fail closed.
			err = commandService.RunProjectOperation(
				authorizedContext,
				func(scopedContext context.Context) error {
					if _, revalidateErr :=
						commandService.
							RevalidatePrincipalProjectOperation(
								scopedContext,
								models.ScopeCommentsWrite,
							); revalidateErr != nil {
						return revalidateErr
					}
					_, createErr := commandService.CreateComment(
						scopedContext,
						fixture.commentInput(
							testCase.source,
							requestDigest,
							reservation.Record.ID,
							"this stale command must roll back",
						),
					)
					return createErr
				},
			)
			if !errors.Is(err, services.ErrPolicyDenied) {
				t.Fatalf(
					"final command after epoch bump error = %v, want ErrPolicyDenied",
					err,
				)
			}
			businessAfter := readPostgresPolicyEpochBusinessState(
				t,
				db,
				fixture,
				reservation.Record.ID,
			)
			if businessAfter != businessBefore {
				t.Fatalf(
					"stale command changed atomic business state:\nbefore=%+v\nafter=%+v",
					businessBefore,
					businessAfter,
				)
			}
			if decisionsAfter := countPostgresPolicyEpochDecisions(
				t,
				db,
				fixture,
			); decisionsAfter != decisionsBefore {
				t.Fatalf(
					"failed final command persisted PolicyDecision rows: before=%d after=%d",
					decisionsBefore,
					decisionsAfter,
				)
			}
			if denyPolicy == nil || !denyPolicy.IsActive {
				t.Fatalf(
					"intervening deny policy was not committed: %+v",
					denyPolicy,
				)
			}
		})
	}
}

func TestPostgresFinalPolicyDecisionLockSerializesPolicyMutation(
	t *testing.T,
) {
	for _, testCase := range postgresPolicyEpochProtocolCases() {
		t.Run(testCase.name, func(t *testing.T) {
			db, _, project, suffix :=
				openPostgresAuthorizationBarrierFixture(
					t,
					"pe_lock_"+testCase.fixture,
				)
			bootstrapPostgresTicketConfiguration(t, db)
			fixture := seedPostgresPolicyEpochCommentFixture(
				t,
				db,
				project,
				"lock-"+testCase.fixture+"-"+suffix,
			)
			_, commandDatabases, backendPIDs :=
				openPostgresAuthorizationCommandDatabases(t, db, 2)
			commandService := newPostgresPolicyEpochService(
				t,
				commandDatabases[0],
			)
			policyService := newPostgresPolicyEpochService(
				t,
				commandDatabases[1],
			)
			operationContext := fixture.operationContext(
				t,
				testCase.source,
			)
			reservation, err := commandService.ReserveIdempotency(
				operationContext,
				fixture.actor,
				string(services.NativeCommandCommentCreate),
				"locked-"+testCase.fixture,
				[]byte(`{"content":"commits before deny"}`),
				time.Hour,
			)
			if err != nil {
				t.Fatalf("reserve locked command idempotency: %v", err)
			}
			requestDigest := "policy-epoch-lock-" + testCase.fixture
			authorizedContext, err :=
				commandService.
					AuthorizeNativeCommandInShortProjectTransactions(
						operationContext,
						fixture.authorizationInput(
							testCase.source,
							requestDigest,
						),
					)
			if err != nil {
				t.Fatalf("prepare locked comment authorization: %v", err)
			}

			type commentResult struct {
				result *services.NativeCommentResult
				err    error
			}
			locksHeld := make(chan struct{})
			releaseFinal := make(chan struct{})
			finalReleased := false
			defer func() {
				if !finalReleased {
					close(releaseFinal)
				}
			}()
			finalResults := make(chan commentResult, 1)
			go func() {
				var result *services.NativeCommentResult
				runErr := commandService.RunProjectOperation(
					authorizedContext,
					func(scopedContext context.Context) error {
						if _, revalidateErr :=
							commandService.
								RevalidatePrincipalProjectOperation(
									scopedContext,
									models.ScopeCommentsWrite,
								); revalidateErr != nil {
							return revalidateErr
						}
						close(locksHeld)
						select {
						case <-releaseFinal:
						case <-scopedContext.Done():
							return scopedContext.Err()
						}
						var createErr error
						result, createErr = commandService.CreateComment(
							scopedContext,
							fixture.commentInput(
								testCase.source,
								requestDigest,
								reservation.Record.ID,
								"committed before the deny epoch",
							),
						)
						return createErr
					},
				)
				finalResults <- commentResult{
					result: result,
					err:    runErr,
				}
			}()
			select {
			case <-locksHeld:
			case result := <-finalResults:
				t.Fatalf(
					"final command completed before lock barrier: %+v",
					result,
				)
			case <-time.After(5 * time.Second):
				t.Fatal("final command did not acquire authorization locks")
			}

			type policyResult struct {
				policy *models.AgentPolicy
				err    error
			}
			policyCompleted := make(chan struct{})
			policyResults := make(chan policyResult, 1)
			go func() {
				defer close(policyCompleted)
				policy, createErr := policyService.CreateAgentPolicy(
					operationContext,
					fixture.denyPolicyInput(
						"deny commands after locked commit",
					),
				)
				policyResults <- policyResult{
					policy: policy,
					err:    createErr,
				}
			}()

			// The final project transaction holds FOR SHARE on the principal.
			// Production CreateAgentPolicy requires FOR UPDATE on that row, so
			// PostgreSQL itself must report the policy backend waiting on Lock.
			waitForPostgresBackendLock(
				t,
				db,
				backendPIDs[1],
				policyCompleted,
			)
			close(releaseFinal)
			finalReleased = true

			var committedComment *services.NativeCommentResult
			select {
			case result := <-finalResults:
				if result.err != nil {
					t.Fatalf(
						"final command before policy commit: %v",
						result.err,
					)
				}
				if result.result == nil ||
					result.result.Comment == nil ||
					result.result.Event == nil {
					t.Fatalf(
						"final command result is incomplete: %+v",
						result.result,
					)
				}
				committedComment = result.result
			case <-time.After(5 * time.Second):
				t.Fatal("final command did not commit after barrier release")
			}
			var committedPolicy *models.AgentPolicy
			select {
			case result := <-policyResults:
				if result.err != nil {
					t.Fatalf(
						"policy mutation after final commit: %v",
						result.err,
					)
				}
				if result.policy == nil || !result.policy.IsActive {
					t.Fatalf(
						"committed deny policy is incomplete: %+v",
						result.policy,
					)
				}
				committedPolicy = result.policy
			case <-time.After(5 * time.Second):
				t.Fatal("policy mutation remained blocked after final commit")
			}
			assertPostgresPolicyEpoch(
				t,
				db,
				fixture.machine.principal.ID,
				fixture.initialPolicyEpoch+1,
			)

			committedState := readPostgresPolicyEpochBusinessState(
				t,
				db,
				fixture,
				reservation.Record.ID,
			)
			if committedState.TicketVersion != fixture.ticket.Version+1 ||
				committedState.TicketCommentCount !=
					fixture.ticket.CommentCount+1 ||
				committedState.CommentCount != 1 ||
				committedState.EventCount != 1 ||
				committedState.OutboxCount != 1 ||
				committedState.IdempotencyState !=
					models.IdempotencyStateCompleted ||
				committedState.IdempotencyEventID !=
					committedComment.Event.ID {
				t.Fatalf(
					"final command did not commit atomically before deny: %+v",
					committedState,
				)
			}

			nextDigest := requestDigest + "-next"
			_, err = commandService.
				AuthorizeNativeCommandInShortProjectTransactions(
					operationContext,
					fixture.authorizationInput(
						testCase.source,
						nextDigest,
					),
				)
			if !errors.Is(err, services.ErrPolicyDenied) {
				t.Fatalf(
					"next command after deny commit error = %v, want ErrPolicyDenied",
					err,
				)
			}
			deniedDecision := loadPostgresPolicyEpochDecision(
				t,
				db,
				fixture,
				testCase.source,
				nextDigest,
			)
			if deniedDecision.Allowed ||
				deniedDecision.ReasonCode != "explicit_deny" ||
				deniedDecision.MatchedPolicyID != committedPolicy.ID ||
				deniedDecision.PolicyEpoch !=
					fixture.initialPolicyEpoch+1 {
				t.Fatalf(
					"next command decision did not observe committed deny epoch: %+v",
					deniedDecision,
				)
			}
			afterDeniedState := readPostgresPolicyEpochBusinessState(
				t,
				db,
				fixture,
				reservation.Record.ID,
			)
			if afterDeniedState != committedState {
				t.Fatalf(
					"denied next command changed business state:\nbefore=%+v\nafter=%+v",
					committedState,
					afterDeniedState,
				)
			}
		})
	}
}

type postgresPolicyEpochProtocolCase struct {
	name    string
	fixture string
	source  services.SourceProtocol
}

func postgresPolicyEpochProtocolCases() []postgresPolicyEpochProtocolCase {
	return []postgresPolicyEpochProtocolCase{
		{
			name:    "Agent_REST",
			fixture: "ar",
			source:  services.SourceProtocolAgentREST,
		},
		{
			name:    "MCP",
			fixture: "mcp",
			source:  services.SourceProtocolMCP,
		},
		{
			name:    "A2A",
			fixture: "a2a",
			source:  services.SourceProtocolA2A,
		},
	}
}

type postgresPolicyEpochCommentFixture struct {
	machine            postgresMachineAuthorizationFixture
	actor              models.ActorRef
	ticket             models.Ticket
	lease              models.TicketLease
	initialPolicyEpoch uint64
}

func seedPostgresPolicyEpochCommentFixture(
	t *testing.T,
	db *gorm.DB,
	project models.Project,
	suffix string,
) postgresPolicyEpochCommentFixture {
	t.Helper()
	machine := seedPostgresMachineAuthorizationFixture(
		t,
		db,
		project,
		"policy-epoch-"+suffix,
	)
	commentScopes := datatypes.JSON(`["comments:write"]`)
	if err := db.Model(&models.ServicePrincipal{}).
		Where("id = ?", machine.principal.ID).
		Update("scopes", commentScopes).Error; err != nil {
		t.Fatalf("grant Principal comment scope: %v", err)
	}
	if err := db.Model(&models.ProjectPrincipalGrant{}).
		Where("id = ?", machine.grant.ID).
		Update("scopes", commentScopes).Error; err != nil {
		t.Fatalf("grant project comment scope: %v", err)
	}
	machine.principal.Scopes = commentScopes
	machine.grant.Scopes = commentScopes

	_, ticket, lease := seedPostgresTicketLeaseFixture(
		t,
		db,
		project,
		suffix,
		time.Now().UTC().Add(10*time.Minute),
	)
	actor := models.ServicePrincipalActor(machine.principal.ID)
	if err := db.Model(&models.TicketLease{}).
		Where("id = ?", lease.ID).
		Updates(map[string]any{
			"holder_actor_type": actor.Type,
			"holder_actor_id":   actor.ID,
		}).Error; err != nil {
		t.Fatalf("transfer Ticket Lease to Principal: %v", err)
	}
	lease.HolderActorType = actor.Type
	lease.HolderActorID = actor.ID

	var persistedPrincipal models.ServicePrincipal
	if err := db.First(
		&persistedPrincipal,
		"id = ?",
		machine.principal.ID,
	).Error; err != nil {
		t.Fatalf("reload policy epoch Principal: %v", err)
	}
	if persistedPrincipal.PolicyEpoch == 0 {
		t.Fatal("seeded Principal has a zero policy-set epoch")
	}
	machine.principal = persistedPrincipal
	return postgresPolicyEpochCommentFixture{
		machine:            machine,
		actor:              actor,
		ticket:             ticket,
		lease:              lease,
		initialPolicyEpoch: persistedPrincipal.PolicyEpoch,
	}
}

func newPostgresPolicyEpochService(
	t *testing.T,
	db *gorm.DB,
) *services.AgentNativeService {
	t.Helper()
	ledger, err := services.NewAuditLedgerService(db)
	if err != nil {
		t.Fatalf("create policy epoch Audit service: %v", err)
	}
	return services.NewAgentNativeService(
		db,
		services.AgentNativeOptions{AuditLedger: ledger},
	)
}

func (fixture postgresPolicyEpochCommentFixture) operationContext(
	t *testing.T,
	source services.SourceProtocol,
) context.Context {
	t.Helper()
	return fixture.machine.operationContextForSource(t, source)
}

func (fixture postgresPolicyEpochCommentFixture) authorizationInput(
	source services.SourceProtocol,
	requestDigest string,
) services.NativeCommandAuthorizationInput {
	return services.NativeCommandAuthorizationInput{
		Kind:         services.NativeCommandCommentCreate,
		Actor:        fixture.actor,
		CredentialID: fixture.machine.credential.ID,
		TokenScopes: []string{
			models.ScopeCommentsWrite,
		},
		TicketID:       fixture.ticket.ID,
		LeaseID:        fixture.lease.ID,
		RequestDigest:  requestDigest,
		SourceProtocol: string(source),
	}
}

func (fixture postgresPolicyEpochCommentFixture) commentInput(
	source services.SourceProtocol,
	requestDigest string,
	idempotencyRecordID string,
	content string,
) services.NativeCommentInput {
	return services.NativeCommentInput{
		TicketID:                 fixture.ticket.ID,
		ExpectedVersion:          fixture.ticket.Version,
		LeaseID:                  fixture.lease.ID,
		Actor:                    fixture.actor,
		CredentialID:             fixture.machine.credential.ID,
		SourceProtocol:           string(source),
		RequestDigest:            requestDigest,
		Content:                  content,
		ContentType:              "markdown",
		Type:                     models.CommentTypeInternal,
		Reason:                   "PostgreSQL policy epoch barrier regression",
		IdempotencyRecordID:      idempotencyRecordID,
		IdempotencyCompletionTTL: time.Hour,
	}
}

func (fixture postgresPolicyEpochCommentFixture) denyPolicyInput(
	name string,
) services.CreateAgentPolicyInput {
	return services.CreateAgentPolicyInput{
		ServicePrincipalID: fixture.machine.principal.ID,
		Name:               name,
		Effect:             models.AgentPolicyEffectDeny,
		Scope:              models.ScopeCommentsWrite,
		Action:             "ticket.comment.create",
		ResourceType:       "ticket",
		ResourceID: strconv.FormatUint(
			uint64(fixture.ticket.ID),
			10,
		),
		Priority: 1000,
	}
}

type postgresPolicyEpochBusinessState struct {
	TicketVersion              uint64
	TicketTitle                string
	TicketCommentCount         int
	LeaseTicketVersion         uint64
	CommentCount               int64
	HistoryCount               int64
	EventCount                 int64
	OutboxCount                int64
	IdempotencyCount           int64
	IdempotencyState           models.IdempotencyState
	IdempotencyResponseCode    int
	IdempotencyResponseBody    string
	IdempotencySnapshot        string
	IdempotencyResourceID      string
	IdempotencyEventID         string
	IdempotencyLastErrorCode   string
	IdempotencyCompleted       bool
	IdempotencyUpdatedUnixNano int64
}

func readPostgresPolicyEpochBusinessState(
	t *testing.T,
	db *gorm.DB,
	fixture postgresPolicyEpochCommentFixture,
	idempotencyRecordID string,
) postgresPolicyEpochBusinessState {
	t.Helper()
	var state postgresPolicyEpochBusinessState
	err := WithProjectScopeTransaction(
		context.Background(),
		db,
		fixture.machine.project.Scope(),
		func(scoped *gorm.DB) error {
			var ticket models.Ticket
			if err := scoped.First(
				&ticket,
				fixture.ticket.ID,
			).Error; err != nil {
				return fmt.Errorf("load policy epoch Ticket: %w", err)
			}
			state.TicketVersion = ticket.Version
			state.TicketTitle = ticket.Title
			state.TicketCommentCount = ticket.CommentCount

			var lease models.TicketLease
			if err := scoped.First(
				&lease,
				"id = ?",
				fixture.lease.ID,
			).Error; err != nil {
				return fmt.Errorf("load policy epoch Ticket Lease: %w", err)
			}
			state.LeaseTicketVersion = lease.TicketVersion
			if err := scoped.Model(&models.TicketComment{}).
				Where("ticket_id = ?", fixture.ticket.ID).
				Count(&state.CommentCount).Error; err != nil {
				return fmt.Errorf("count policy epoch Comments: %w", err)
			}
			if err := scoped.Model(&models.TicketHistory{}).
				Where("ticket_id = ?", fixture.ticket.ID).
				Count(&state.HistoryCount).Error; err != nil {
				return fmt.Errorf("count policy epoch History rows: %w", err)
			}
			if err := scoped.Model(&models.DomainEvent{}).
				Where(
					"subject = ?",
					fmt.Sprintf("ticket/%d", fixture.ticket.ID),
				).
				Count(&state.EventCount).Error; err != nil {
				return fmt.Errorf("count policy epoch Events: %w", err)
			}
			if err := scoped.Model(&models.OutboxDelivery{}).
				Count(&state.OutboxCount).Error; err != nil {
				return fmt.Errorf("count policy epoch Outbox rows: %w", err)
			}
			if err := scoped.Model(&models.IdempotencyRecord{}).
				Where(
					"actor_type = ? AND actor_id = ?",
					fixture.actor.Type,
					fixture.actor.ID,
				).
				Count(&state.IdempotencyCount).Error; err != nil {
				return fmt.Errorf(
					"count policy epoch Idempotency rows: %w",
					err,
				)
			}
			var record models.IdempotencyRecord
			if err := scoped.First(
				&record,
				"id = ?",
				idempotencyRecordID,
			).Error; err != nil {
				return fmt.Errorf(
					"load policy epoch Idempotency row: %w",
					err,
				)
			}
			state.IdempotencyState = record.State
			state.IdempotencyResponseCode = record.ResponseCode
			state.IdempotencyResponseBody = string(record.ResponseBody)
			state.IdempotencySnapshot = string(record.ResourceSnapshot)
			state.IdempotencyResourceID = record.ResourceID
			state.IdempotencyEventID = record.EventID
			state.IdempotencyLastErrorCode = record.LastErrorCode
			state.IdempotencyCompleted = record.CompletedAt != nil
			state.IdempotencyUpdatedUnixNano =
				record.UpdatedAt.UTC().UnixNano()
			return nil
		},
	)
	if err != nil {
		t.Fatalf("read policy epoch business state: %v", err)
	}
	return state
}

func loadPostgresPolicyEpochDecision(
	t *testing.T,
	db *gorm.DB,
	fixture postgresPolicyEpochCommentFixture,
	source services.SourceProtocol,
	requestDigest string,
) models.PolicyDecision {
	t.Helper()
	var decision models.PolicyDecision
	err := WithProjectScopeTransaction(
		context.Background(),
		db,
		fixture.machine.project.Scope(),
		func(scoped *gorm.DB) error {
			return scoped.
				Where(
					"service_principal_id = ? AND action = ? AND resource_id = ? AND request_digest = ? AND source_protocol = ?",
					fixture.machine.principal.ID,
					"ticket.comment.create",
					strconv.FormatUint(
						uint64(fixture.ticket.ID),
						10,
					),
					requestDigest,
					string(source),
				).
				Order("created_at DESC").
				Take(&decision).Error
		},
	)
	if err != nil {
		t.Fatalf("load policy epoch PolicyDecision: %v", err)
	}
	return decision
}

func countPostgresPolicyEpochDecisions(
	t *testing.T,
	db *gorm.DB,
	fixture postgresPolicyEpochCommentFixture,
) int64 {
	t.Helper()
	var count int64
	err := WithProjectScopeTransaction(
		context.Background(),
		db,
		fixture.machine.project.Scope(),
		func(scoped *gorm.DB) error {
			return scoped.Model(&models.PolicyDecision{}).
				Where(
					"service_principal_id = ?",
					fixture.machine.principal.ID,
				).
				Count(&count).Error
		},
	)
	if err != nil {
		t.Fatalf("count policy epoch PolicyDecision rows: %v", err)
	}
	return count
}

func assertPostgresPolicyEpoch(
	t *testing.T,
	db *gorm.DB,
	principalID string,
	want uint64,
) {
	t.Helper()
	var principal models.ServicePrincipal
	if err := db.First(
		&principal,
		"id = ?",
		principalID,
	).Error; err != nil {
		t.Fatalf("reload policy epoch Principal: %v", err)
	}
	if principal.PolicyEpoch != want {
		t.Fatalf(
			"Principal policy epoch = %d, want %d",
			principal.PolicyEpoch,
			want,
		)
	}
}

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type ticketRelationshipOperationsStub struct {
	addEntityLink     func(context.Context, services.AddEntityLinkInput) (*services.AddEntityLinkResult, error)
	addTicketRelation func(context.Context, services.AddTicketRelationInput) (*services.AddTicketRelationResult, error)
	listEntityLinks   func(
		context.Context,
		uint,
		services.DirectoryPageRequest,
	) (*services.DirectoryPage[models.EntityLink], error)
	listTicketRelations func(
		context.Context,
		uint,
		services.DirectoryPageRequest,
	) (*services.DirectoryPage[services.TicketRelationDirectoryItem], error)
}

func (stub *ticketRelationshipOperationsStub) AddEntityLink(
	ctx context.Context,
	input services.AddEntityLinkInput,
) (*services.AddEntityLinkResult, error) {
	if stub.addEntityLink == nil {
		return nil, errors.New("unexpected AddEntityLink call")
	}
	return stub.addEntityLink(ctx, input)
}

func (stub *ticketRelationshipOperationsStub) AddTicketRelation(
	ctx context.Context,
	input services.AddTicketRelationInput,
) (*services.AddTicketRelationResult, error) {
	if stub.addTicketRelation == nil {
		return nil, errors.New("unexpected AddTicketRelation call")
	}
	return stub.addTicketRelation(ctx, input)
}

func (stub *ticketRelationshipOperationsStub) ListEntityLinks(
	ctx context.Context,
	ticketID uint,
	request services.DirectoryPageRequest,
) (*services.DirectoryPage[models.EntityLink], error) {
	if stub.listEntityLinks == nil {
		return nil, errors.New("unexpected ListEntityLinks call")
	}
	return stub.listEntityLinks(ctx, ticketID, request)
}

func (stub *ticketRelationshipOperationsStub) ListTicketRelations(
	ctx context.Context,
	ticketID uint,
	request services.DirectoryPageRequest,
) (*services.DirectoryPage[services.TicketRelationDirectoryItem], error) {
	if stub.listTicketRelations == nil {
		return nil, errors.New("unexpected ListTicketRelations call")
	}
	return stub.listTicketRelations(ctx, ticketID, request)
}

type ticketRelationshipTicketServiceStub struct {
	services.TicketServiceInterface
	getTicket func(context.Context, uint) (*models.Ticket, error)
}

func (stub ticketRelationshipTicketServiceStub) GetTicket(
	ctx context.Context,
	ticketID uint,
) (*models.Ticket, error) {
	if stub.getTicket == nil {
		return nil, errors.New("unexpected GetTicket call")
	}
	return stub.getTicket(ctx, ticketID)
}

func TestTicketRelationshipHandlerRegistersOnlyProjectScopedRoutes(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	handler := newTicketRelationshipHandler(
		&ticketRelationshipOperationsStub{},
		ticketRelationshipTicketServiceStub{},
	)
	router := gin.New()
	projectGroup := router.Group("/api/projects/:projectKey")
	handler.RegisterRoutes(projectGroup)
	// Match the production registration order and prove these routes can share
	// the canonical ticket wildcard without making Gin panic at startup.
	projectGroup.GET("/tickets/:id", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	got := make(map[string]struct{})
	for _, route := range router.Routes() {
		got[route.Method+" "+route.Path] = struct{}{}
	}
	for _, route := range []string{
		"GET /api/projects/:projectKey/tickets/:id/entity-links",
		"POST /api/projects/:projectKey/tickets/:id/entity-links",
		"GET /api/projects/:projectKey/tickets/:id/relations",
		"POST /api/projects/:projectKey/tickets/:id/relations",
	} {
		if _, ok := got[route]; !ok {
			t.Fatalf("project-scoped route %q was not registered: %#v", route, got)
		}
	}

	for _, path := range []string{
		"/api/tickets/41/entity-links",
		"/api/tickets/41/relations",
		"/api/projects/OPS/entity-links",
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, path, nil),
		)
		if response.Code != http.StatusNotFound {
			t.Fatalf("implicit/global route %q status=%d, want 404", path, response.Code)
		}
	}
}

func TestTicketRelationshipHandlerRequiresMatchingOperationContext(
	t *testing.T,
) {
	scope := models.ProjectScope{OrganizationID: 7, ProjectID: 70}
	access := ticketRelationshipTestAccess(scope, "OPS", models.ProjectRoleManager)
	var ticketCalls int
	var relationshipCalls int
	handler := newTicketRelationshipHandler(
		&ticketRelationshipOperationsStub{
			listEntityLinks: func(
				context.Context,
				uint,
				services.DirectoryPageRequest,
			) (*services.DirectoryPage[models.EntityLink], error) {
				relationshipCalls++
				return nil, nil
			},
		},
		ticketRelationshipTicketServiceStub{
			getTicket: func(
				context.Context,
				uint,
			) (*models.Ticket, error) {
				ticketCalls++
				return &models.Ticket{ID: 41, Version: 3}, nil
			},
		},
	)
	cases := []struct {
		name      string
		operation *services.OperationContext
	}{
		{name: "missing"},
		{
			name: "mismatched scope",
			operation: &services.OperationContext{
				Scope: models.ProjectScope{
					OrganizationID: scope.OrganizationID,
					ProjectID:      scope.ProjectID + 1,
				},
				Actor:  models.HumanActor(9),
				Source: services.SourceProtocolHumanREST,
			},
		},
		{
			name: "mismatched actor",
			operation: &services.OperationContext{
				Scope:  scope,
				Actor:  models.HumanActor(10),
				Source: services.SourceProtocolHumanREST,
			},
		},
		{
			name: "mismatched protocol",
			operation: &services.OperationContext{
				Scope:  scope,
				Actor:  models.HumanActor(9),
				Source: services.SourceProtocolConnector,
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			router := ticketRelationshipTestRouter(
				t,
				handler,
				access,
				9,
				test.operation,
			)
			response := httptest.NewRecorder()
			router.ServeHTTP(
				response,
				httptest.NewRequest(
					http.MethodGet,
					"/api/projects/OPS/tickets/41/entity-links",
					nil,
				),
			)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			assertTicketRelationshipProblemCode(
				t,
				response,
				"ticket_relationship_access_denied",
			)
		})
	}
	if ticketCalls != 0 || relationshipCalls != 0 {
		t.Fatalf(
			"untrusted contexts reached services: ticket=%d relationship=%d",
			ticketCalls,
			relationshipCalls,
		)
	}
}

func TestTicketRelationshipHandlerCreatesEntityLinksAndRelations(
	t *testing.T,
) {
	scope := models.ProjectScope{OrganizationID: 8, ProjectID: 80}
	operation := services.OperationContext{
		Scope:         scope,
		Actor:         models.HumanActor(11),
		Source:        services.SourceProtocolHumanREST,
		TraceID:       "trace-relationship",
		CorrelationID: "correlation-relationship",
	}
	access := ticketRelationshipTestAccess(scope, "OPS", models.ProjectRoleManager)

	t.Run("entity link", func(t *testing.T) {
		var received services.AddEntityLinkInput
		relationships := &ticketRelationshipOperationsStub{
			addEntityLink: func(
				ctx context.Context,
				input services.AddEntityLinkInput,
			) (*services.AddEntityLinkResult, error) {
				assertTicketRelationshipOperation(t, ctx, operation)
				received = input
				return &services.AddEntityLinkResult{
					Link: &models.EntityLink{
						ID:             "019891f0-b78b-7d58-a5f1-00f6cbcf0dc1",
						CreatedAt:      time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
						OrganizationID: scope.OrganizationID,
						ProjectID:      scope.ProjectID,
						TicketID:       input.TicketID,
						Kind:           input.Kind,
						ReferenceID:    input.ReferenceID,
						DisplayName:    input.DisplayName,
						Metadata:       datatypes.JSON([]byte(`{"zone":"cn-east"}`)),
						CreatedByType:  models.ActorTypeHuman,
						CreatedByID:    "PRIVATE-ACTOR-ID",
					},
					TicketVersion: 8,
					EventID:       "event-entity-linked",
				}, nil
			},
		}
		handler := newTicketRelationshipHandler(
			relationships,
			ticketRelationshipTicketServiceStub{
				getTicket: func(
					ctx context.Context,
					ticketID uint,
				) (*models.Ticket, error) {
					assertTicketRelationshipOperation(t, ctx, operation)
					return &models.Ticket{ID: ticketID, Version: 7}, nil
				},
			},
		)
		router := ticketRelationshipTestRouter(
			t,
			handler,
			access,
			11,
			&operation,
		)
		response := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/projects/OPS/tickets/41/entity-links",
			bytes.NewBufferString(`{
				"expected_version": 7,
				"kind": "asset",
				"reference_id": " cmdb:asset/42 ",
				"display_name": " 核心交换机 ",
				"metadata": {"zone":"cn-east"}
			}`),
		)
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)

		if response.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", response.Code, response.Body)
		}
		if response.Header().Get("ETag") != httpcontract.FormatETag(8) {
			t.Fatalf("ETag=%q, want v8", response.Header().Get("ETag"))
		}
		if received.TicketID != 41 ||
			received.ExpectedVersion != 7 ||
			received.Kind != models.EntityKindAsset ||
			received.ReferenceID != "cmdb:asset/42" ||
			received.DisplayName != "核心交换机" {
			t.Fatalf("AddEntityLink input = %+v", received)
		}
		assertTicketRelationshipResponseIsSafe(t, response.Body.String())
		var body struct {
			Data addEntityLinkResponse `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Data.TicketVersion != 8 ||
			body.Data.EventID != "event-entity-linked" ||
			body.Data.Link.TicketID != 41 ||
			body.Data.Link.Metadata["zone"] != "cn-east" {
			t.Fatalf("entity link response = %+v", body.Data)
		}
	})

	t.Run("ticket relation", func(t *testing.T) {
		var received services.AddTicketRelationInput
		var lookedUp []uint
		relationships := &ticketRelationshipOperationsStub{
			addTicketRelation: func(
				ctx context.Context,
				input services.AddTicketRelationInput,
			) (*services.AddTicketRelationResult, error) {
				assertTicketRelationshipOperation(t, ctx, operation)
				received = input
				return &services.AddTicketRelationResult{
					Relation: &models.TicketRelation{
						ID:             "019891f1-3462-733c-aad6-006f7391cfbd",
						CreatedAt:      time.Date(2026, 7, 30, 8, 1, 0, 0, time.UTC),
						OrganizationID: scope.OrganizationID,
						ProjectID:      scope.ProjectID,
						SourceTicketID: input.SourceTicketID,
						TargetTicketID: input.TargetTicketID,
						Relation:       input.Relation,
						Reason:         input.Reason,
						CreatedByType:  models.ActorTypeHuman,
						CreatedByID:    "PRIVATE-ACTOR-ID",
					},
					TicketVersion: 9,
					EventID:       "event-relation-created",
				}, nil
			},
		}
		handler := newTicketRelationshipHandler(
			relationships,
			ticketRelationshipTicketServiceStub{
				getTicket: func(
					ctx context.Context,
					ticketID uint,
				) (*models.Ticket, error) {
					assertTicketRelationshipOperation(t, ctx, operation)
					lookedUp = append(lookedUp, ticketID)
					return &models.Ticket{ID: ticketID, Version: 8}, nil
				},
			},
		)
		router := ticketRelationshipTestRouter(
			t,
			handler,
			access,
			11,
			&operation,
		)
		response := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/projects/OPS/tickets/41/relations",
			bytes.NewBufferString(`{
				"expected_version": 8,
				"target_ticket_id": 42,
				"relation": "blocks",
				"reason": " 等待网络变更 "
			}`),
		)
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)

		if response.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", response.Code, response.Body)
		}
		if response.Header().Get("ETag") != httpcontract.FormatETag(9) {
			t.Fatalf("ETag=%q, want v9", response.Header().Get("ETag"))
		}
		if len(lookedUp) != 2 || lookedUp[0] != 41 || lookedUp[1] != 42 {
			t.Fatalf("ticket authorization lookups = %v, want [41 42]", lookedUp)
		}
		if received.SourceTicketID != 41 ||
			received.TargetTicketID != 42 ||
			received.ExpectedVersion != 8 ||
			received.Relation != models.TicketRelationBlocks ||
			received.Reason != "等待网络变更" {
			t.Fatalf("AddTicketRelation input = %+v", received)
		}
		assertTicketRelationshipResponseIsSafe(t, response.Body.String())
		var body struct {
			Data addTicketRelationResponse `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Data.TicketVersion != 9 ||
			body.Data.EventID != "event-relation-created" ||
			body.Data.Relation.SourceTicketID != 41 ||
			body.Data.Relation.TargetTicketID != 42 {
			t.Fatalf("ticket relation response = %+v", body.Data)
		}
	})
}

func TestTicketRelationshipHandlerListsSafeProjectScopedViews(
	t *testing.T,
) {
	scope := models.ProjectScope{OrganizationID: 9, ProjectID: 90}
	operation := services.OperationContext{
		Scope:  scope,
		Actor:  models.HumanActor(12),
		Source: services.SourceProtocolHumanREST,
	}
	access := ticketRelationshipTestAccess(scope, "OPS", models.ProjectRoleObserver)
	var entityCalls int
	var relationCalls int
	handler := newTicketRelationshipHandler(
		&ticketRelationshipOperationsStub{
			listEntityLinks: func(
				ctx context.Context,
				ticketID uint,
				request services.DirectoryPageRequest,
			) (*services.DirectoryPage[models.EntityLink], error) {
				assertTicketRelationshipOperation(t, ctx, operation)
				entityCalls++
				if request.Page != 1 || request.PageSize != 25 ||
					request.SortBy != "created_at" ||
					request.SortOrder != "desc" {
					t.Fatalf("entity link page request = %+v", request)
				}
				return &services.DirectoryPage[models.EntityLink]{
					Items: []models.EntityLink{{
						ID:             "019891f1-e935-72db-8719-36e0cbb29688",
						OrganizationID: scope.OrganizationID,
						ProjectID:      scope.ProjectID,
						TicketID:       ticketID,
						Kind:           models.EntityKindDevice,
						ReferenceID:    "device:edge-1",
						DisplayName:    "边缘网关",
						Metadata:       datatypes.JSON([]byte(`{"rack":"A01"}`)),
						CreatedByType:  models.ActorTypeHuman,
						CreatedByID:    "PRIVATE-LIST-ACTOR",
					}},
					Total: 1, Page: 1, PageSize: 25, TotalPages: 1,
				}, nil
			},
			listTicketRelations: func(
				ctx context.Context,
				ticketID uint,
				request services.DirectoryPageRequest,
			) (*services.DirectoryPage[services.TicketRelationDirectoryItem], error) {
				assertTicketRelationshipOperation(t, ctx, operation)
				relationCalls++
				if request.Page != 1 || request.PageSize != 25 ||
					request.SortBy != "created_at" ||
					request.SortOrder != "desc" {
					t.Fatalf("ticket relation page request = %+v", request)
				}
				return &services.DirectoryPage[services.TicketRelationDirectoryItem]{
					Items: []services.TicketRelationDirectoryItem{{
						Relation: models.TicketRelation{
							ID:             "019891f2-6228-7ebc-ad60-82f11121c6a4",
							OrganizationID: scope.OrganizationID,
							ProjectID:      scope.ProjectID,
							SourceTicketID: ticketID,
							TargetTicketID: 42,
							Relation:       models.TicketRelationCollaboratesWith,
							Reason:         "联合处理",
							CreatedByType:  models.ActorTypeHuman,
							CreatedByID:    "PRIVATE-LIST-ACTOR",
						},
						Direction:           services.TicketRelationDirectionOutgoing,
						RelatedTicketID:     42,
						RelatedTicketNumber: "OPS-42",
						RelatedTicketTitle:  "数据库协作工单",
					}},
					Total: 1, Page: 1, PageSize: 25, TotalPages: 1,
				}, nil
			},
		},
		ticketRelationshipTicketServiceStub{
			getTicket: func(
				ctx context.Context,
				ticketID uint,
			) (*models.Ticket, error) {
				assertTicketRelationshipOperation(t, ctx, operation)
				return &models.Ticket{ID: ticketID, Version: 10}, nil
			},
		},
	)
	router := ticketRelationshipTestRouter(
		t,
		handler,
		access,
		12,
		&operation,
	)

	for _, resource := range []string{"entity-links", "relations"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(
				http.MethodGet,
				"/api/projects/OPS/tickets/41/"+resource,
				nil,
			),
		)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", resource, response.Code, response.Body)
		}
		if response.Header().Get("ETag") != httpcontract.FormatETag(10) {
			t.Fatalf("%s ETag=%q", resource, response.Header().Get("ETag"))
		}
		assertTicketRelationshipResponseIsSafe(t, response.Body.String())
		var body struct {
			Data struct {
				Items         []map[string]any `json:"items"`
				Total         int              `json:"total"`
				Page          int              `json:"page"`
				PageSize      int              `json:"page_size"`
				TotalPages    int              `json:"total_pages"`
				TicketVersion uint64           `json:"ticket_version"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Data.Total != 1 || body.Data.TicketVersion != 10 ||
			body.Data.Page != 1 || body.Data.PageSize != 25 ||
			body.Data.TotalPages != 1 || len(body.Data.Items) != 1 {
			t.Fatalf("%s list response = %+v", resource, body)
		}
	}
	if entityCalls != 1 || relationCalls != 1 {
		t.Fatalf(
			"list calls entity=%d relation=%d, want one each",
			entityCalls,
			relationCalls,
		)
	}
}

func TestTicketRelationshipListsRejectInvalidPagination(t *testing.T) {
	scope := models.ProjectScope{OrganizationID: 19, ProjectID: 190}
	operation := services.OperationContext{
		Scope:  scope,
		Actor:  models.HumanActor(19),
		Source: services.SourceProtocolHumanREST,
	}
	access := ticketRelationshipTestAccess(
		scope,
		"OPS",
		models.ProjectRoleObserver,
	)
	var listCalls int
	handler := newTicketRelationshipHandler(
		&ticketRelationshipOperationsStub{
			listEntityLinks: func(
				context.Context,
				uint,
				services.DirectoryPageRequest,
			) (*services.DirectoryPage[models.EntityLink], error) {
				listCalls++
				return nil, errors.New("unexpected list call")
			},
		},
		ticketRelationshipTicketServiceStub{
			getTicket: func(
				context.Context,
				uint,
			) (*models.Ticket, error) {
				return &models.Ticket{ID: 41, Version: 1}, nil
			},
		},
	)
	for _, query := range []string{
		"page=0",
		"page=-1",
		"page_size=101",
		"page=",
		"page=1&page=2",
		"sort_by=id",
		"sort_order=DESC",
		"unknown=value",
		"page=%ZZ",
	} {
		t.Run(query, func(t *testing.T) {
			router := ticketRelationshipTestRouter(
				t,
				handler,
				access,
				19,
				&operation,
			)
			response := httptest.NewRecorder()
			router.ServeHTTP(
				response,
				httptest.NewRequest(
					http.MethodGet,
					"/api/projects/OPS/tickets/41/entity-links?"+query,
					nil,
				),
			)
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"query=%q status=%d body=%s",
					query,
					response.Code,
					response.Body,
				)
			}
		})
	}
	if listCalls != 0 {
		t.Fatalf("invalid pagination reached relationship service %d times", listCalls)
	}
}

func TestTicketRelationshipHandlerRejectsInvalidWriteContracts(
	t *testing.T,
) {
	scope := models.ProjectScope{OrganizationID: 10, ProjectID: 100}
	operation := services.OperationContext{
		Scope:  scope,
		Actor:  models.HumanActor(13),
		Source: services.SourceProtocolHumanREST,
	}
	access := ticketRelationshipTestAccess(scope, "OPS", models.ProjectRoleManager)
	var relationshipCalls int
	handler := newTicketRelationshipHandler(
		&ticketRelationshipOperationsStub{
			addEntityLink: func(
				context.Context,
				services.AddEntityLinkInput,
			) (*services.AddEntityLinkResult, error) {
				relationshipCalls++
				return nil, nil
			},
			addTicketRelation: func(
				context.Context,
				services.AddTicketRelationInput,
			) (*services.AddTicketRelationResult, error) {
				relationshipCalls++
				return nil, nil
			},
		},
		ticketRelationshipTicketServiceStub{
			getTicket: func(
				context.Context,
				uint,
			) (*models.Ticket, error) {
				return &models.Ticket{ID: 41, Version: 3}, nil
			},
		},
	)
	cases := []struct {
		name   string
		path   string
		body   string
		status int
		code   string
	}{
		{
			name:   "entity expected version omitted",
			path:   "/api/projects/OPS/tickets/41/entity-links",
			body:   `{"kind":"asset","reference_id":"asset:1","display_name":"A"}`,
			status: http.StatusUnprocessableEntity,
			code:   "expected_version_required",
		},
		{
			name:   "relation expected version zero",
			path:   "/api/projects/OPS/tickets/41/relations",
			body:   `{"expected_version":0,"target_ticket_id":42,"relation":"blocks"}`,
			status: http.StatusUnprocessableEntity,
			code:   "expected_version_required",
		},
		{
			name:   "project field rejected",
			path:   "/api/projects/OPS/tickets/41/entity-links",
			body:   `{"expected_version":3,"kind":"asset","reference_id":"asset:1","display_name":"A","project_id":999}`,
			status: http.StatusBadRequest,
			code:   "invalid_ticket_relationship_request",
		},
		{
			name:   "invalid ticket id",
			path:   "/api/projects/OPS/tickets/not-a-number/entity-links",
			body:   `{}`,
			status: http.StatusBadRequest,
			code:   "invalid_ticket_id",
		},
		{
			name:   "invalid entity kind",
			path:   "/api/projects/OPS/tickets/41/entity-links",
			body:   `{"expected_version":3,"kind":"credential","reference_id":"asset:1","display_name":"A"}`,
			status: http.StatusUnprocessableEntity,
			code:   "invalid_entity_link",
		},
		{
			name:   "self relation",
			path:   "/api/projects/OPS/tickets/41/relations",
			body:   `{"expected_version":3,"target_ticket_id":41,"relation":"blocks"}`,
			status: http.StatusUnprocessableEntity,
			code:   "invalid_ticket_relation",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			router := ticketRelationshipTestRouter(
				t,
				handler,
				access,
				13,
				&operation,
			)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				test.path,
				bytes.NewBufferString(test.body),
			)
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf(
					"status=%d want=%d body=%s",
					response.Code,
					test.status,
					response.Body,
				)
			}
			assertTicketRelationshipProblemCode(t, response, test.code)
		})
	}
	if relationshipCalls != 0 {
		t.Fatalf("invalid requests reached relationship service %d times", relationshipCalls)
	}
}

func TestTicketRelationshipHandlerReturnsStableDomainErrors(t *testing.T) {
	scope := models.ProjectScope{OrganizationID: 11, ProjectID: 110}
	operation := services.OperationContext{
		Scope:  scope,
		Actor:  models.HumanActor(14),
		Source: services.SourceProtocolHumanREST,
	}
	access := ticketRelationshipTestAccess(scope, "OPS", models.ProjectRoleManager)
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			name:   "version conflict",
			err:    services.ErrVersionConflict,
			status: http.StatusConflict,
			code:   "version_conflict",
		},
		{
			name:   "duplicate",
			err:    gorm.ErrDuplicatedKey,
			status: http.StatusConflict,
			code:   "ticket_relationship_conflict",
		},
		{
			name:   "not found",
			err:    gorm.ErrRecordNotFound,
			status: http.StatusNotFound,
			code:   "ticket_relationship_not_found",
		},
		{
			name:   "domain validation",
			err:    errors.New("complete entity link input is required"),
			status: http.StatusUnprocessableEntity,
			code:   "invalid_ticket_relationship",
		},
		{
			name:   "internal",
			err:    errors.New("database unavailable"),
			status: http.StatusInternalServerError,
			code:   "ticket_relationship_internal_error",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			handler := newTicketRelationshipHandler(
				&ticketRelationshipOperationsStub{
					addEntityLink: func(
						context.Context,
						services.AddEntityLinkInput,
					) (*services.AddEntityLinkResult, error) {
						return nil, test.err
					},
				},
				ticketRelationshipTicketServiceStub{
					getTicket: func(
						context.Context,
						uint,
					) (*models.Ticket, error) {
						return &models.Ticket{ID: 41, Version: 3}, nil
					},
				},
			)
			router := ticketRelationshipTestRouter(
				t,
				handler,
				access,
				14,
				&operation,
			)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/projects/OPS/tickets/41/entity-links",
				bytes.NewBufferString(`{
					"expected_version":3,
					"kind":"asset",
					"reference_id":"asset:1",
					"display_name":"A"
				}`),
			)
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf(
					"status=%d want=%d body=%s",
					response.Code,
					test.status,
					response.Body,
				)
			}
			assertTicketRelationshipProblemCode(t, response, test.code)
		})
	}
}

func TestTicketRelationTargetRequiresObjectAuthorization(t *testing.T) {
	scope := models.ProjectScope{OrganizationID: 12, ProjectID: 120}
	operation := services.OperationContext{
		Scope:  scope,
		Actor:  models.HumanActor(15),
		Source: services.SourceProtocolHumanREST,
	}
	access := ticketRelationshipTestAccess(scope, "OPS", models.ProjectRoleRequester)
	var relationshipCalls int
	handler := newTicketRelationshipHandler(
		&ticketRelationshipOperationsStub{
			addTicketRelation: func(
				context.Context,
				services.AddTicketRelationInput,
			) (*services.AddTicketRelationResult, error) {
				relationshipCalls++
				return nil, nil
			},
		},
		ticketRelationshipTicketServiceStub{
			getTicket: func(
				_ context.Context,
				ticketID uint,
			) (*models.Ticket, error) {
				owners := map[uint]uint{41: 15, 42: 99}
				owner := owners[ticketID]
				return &models.Ticket{
					ID:          ticketID,
					Version:     2,
					CreatedByID: &owner,
				}, nil
			},
		},
	)
	router := ticketRelationshipTestRouter(
		t,
		handler,
		access,
		15,
		&operation,
	)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/OPS/tickets/41/relations",
		bytes.NewBufferString(`{
			"expected_version":2,
			"target_ticket_id":42,
			"relation":"duplicate_of"
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	assertTicketRelationshipProblemCode(
		t,
		response,
		"ticket_relationship_access_denied",
	)
	if relationshipCalls != 0 {
		t.Fatal("unauthorized target reached relationship service")
	}
}

func ticketRelationshipTestAccess(
	scope models.ProjectScope,
	projectKey string,
	role models.ProjectRole,
) services.ProjectAccess {
	return services.ProjectAccess{
		Project: models.Project{
			ID:             scope.ProjectID,
			OrganizationID: scope.OrganizationID,
			Key:            models.ProjectKey(projectKey),
			Status:         models.ProjectStatusActive,
		},
		Role:  role,
		Scope: scope,
	}
}

func ticketRelationshipTestRouter(
	t *testing.T,
	handler *TicketRelationshipHandler,
	access services.ProjectAccess,
	userID uint,
	operation *services.OperationContext,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set(projectAccessContextKey, access)
		c.Set(projectRoleContextKey, string(access.Role))
		if operation != nil {
			ctx, err := services.WithOperationContext(
				c.Request.Context(),
				*operation,
			)
			if err != nil {
				t.Fatalf("install operation context: %v", err)
			}
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	})
	handler.RegisterRoutes(group)
	return router
}

func assertTicketRelationshipOperation(
	t *testing.T,
	ctx context.Context,
	want services.OperationContext,
) {
	t.Helper()
	got, err := services.OperationContextFromContext(ctx)
	if err != nil {
		t.Fatalf("read operation context: %v", err)
	}
	if got != want {
		t.Fatalf("operation context = %+v, want %+v", got, want)
	}
}

func assertTicketRelationshipProblemCode(
	t *testing.T,
	response *httptest.ResponseRecorder,
	want string,
) {
	t.Helper()
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v body=%s", err, response.Body)
	}
	if problem.Code != want {
		t.Fatalf("problem code=%q want=%q body=%s", problem.Code, want, response.Body)
	}
	if !strings.HasPrefix(
		response.Header().Get("Content-Type"),
		"application/problem+json",
	) {
		t.Fatalf(
			"problem content type=%q",
			response.Header().Get("Content-Type"),
		)
	}
}

func assertTicketRelationshipResponseIsSafe(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{
		`"organization_id"`,
		`"project_id"`,
		`"created_by_type"`,
		`"created_by_id"`,
		"PRIVATE-ACTOR-ID",
		"PRIVATE-LIST-ACTOR",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("relationship response leaked %q: %s", forbidden, body)
		}
	}
}

func relationshipTicketPath(ticketID uint, resource string) string {
	return "/api/projects/OPS/tickets/" +
		strconv.FormatUint(uint64(ticketID), 10) +
		"/" + resource
}

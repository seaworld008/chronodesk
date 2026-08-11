package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type recordingVersionedTicketService struct {
	services.TicketServiceInterface
	currentVersion uint64
	calls          int
	request        *models.TicketUpdateRequest
}

func (s *recordingVersionedTicketService) result(
	ticketID uint,
	expectedVersion uint64,
) (*models.Ticket, error) {
	s.calls++
	if expectedVersion != s.currentVersion {
		return nil, services.ErrVersionConflict
	}
	return &models.Ticket{
		ID:          ticketID,
		Title:       "versioned ticket",
		Description: "versioned ticket",
		Status:      models.TicketStatusOpen,
		Priority:    models.TicketPriorityHigh,
		Type:        models.TicketTypeRequest,
		Source:      models.TicketSourceWeb,
		Version:     expectedVersion + 1,
	}, nil
}

func (s *recordingVersionedTicketService) UpdateTicketExpectedVersion(
	_ context.Context,
	ticketID uint,
	request *models.TicketUpdateRequest,
	_ uint,
	expectedVersion uint64,
) (*models.Ticket, error) {
	if request != nil {
		copy := *request
		s.request = &copy
	}
	return s.result(ticketID, expectedVersion)
}

func (s *recordingVersionedTicketService) AssignTicketExpectedVersion(
	_ context.Context,
	ticketID uint,
	_ uint,
	_ uint,
	_ string,
	expectedVersion uint64,
) (*models.Ticket, error) {
	return s.result(ticketID, expectedVersion)
}

func (s *recordingVersionedTicketService) TransferTicketExpectedVersion(
	_ context.Context,
	ticketID uint,
	_ uint,
	_ uint,
	_ string,
	_ string,
	expectedVersion uint64,
) (*models.Ticket, error) {
	return s.result(ticketID, expectedVersion)
}

func (s *recordingVersionedTicketService) EscalateTicketExpectedVersion(
	_ context.Context,
	ticketID uint,
	_ uint,
	_ uint,
	_ string,
	_ string,
	expectedVersion uint64,
) (*models.Ticket, error) {
	return s.result(ticketID, expectedVersion)
}

func (s *recordingVersionedTicketService) UpdateTicketStatusExpectedVersion(
	_ context.Context,
	ticketID uint,
	_ string,
	_ uint,
	_ string,
	_ string,
	expectedVersion uint64,
) (*models.Ticket, error) {
	return s.result(ticketID, expectedVersion)
}

type recordingCreateTicketService struct {
	services.TicketServiceInterface
	request *models.TicketCreateRequest
	err     error
}

func (s *recordingCreateTicketService) CreateTicket(
	_ context.Context,
	request *models.TicketCreateRequest,
	userID uint,
) (*models.Ticket, error) {
	copy := *request
	s.request = &copy
	if s.err != nil {
		return nil, s.err
	}
	return &models.Ticket{
		ID:          1,
		Title:       copy.Title,
		Description: copy.Description,
		Status:      models.TicketStatusOpen,
		Priority:    copy.Priority,
		Type:        copy.Type,
		Source:      copy.Source,
		CreatedByID: &userID,
		Version:     1,
	}, nil
}

func TestHumanTicketDetailReturnsStrongETag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	workflow, _, agent, _, ticket, _ := setupWorkflowHandlerTest(t)
	handler := NewTicketHandler(workflow.ticketService)
	router := humanTicketTestRouter(agent, models.ProjectRoleAgent, func(router *gin.Engine) {
		router.GET("/tickets/:id", handler.GetTicket)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10),
			nil,
		),
	)

	if response.Code != http.StatusOK {
		t.Fatalf("GET ticket status=%d body=%s", response.Code, response.Body.String())
	}
	if got, want := response.Header().Get("ETag"), httpcontract.FormatETag(ticket.Version); got != want {
		t.Fatalf("GET ticket ETag=%q, want %q", got, want)
	}
}

func TestHumanTicketPutEnforcesIfMatch(t *testing.T) {
	for _, test := range humanIfMatchCases() {
		test := test
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			workflow, _, agent, _, ticket, _ := setupWorkflowHandlerTest(t)
			service := &recordingVersionedTicketService{
				TicketServiceInterface: workflow.ticketService,
				currentVersion:         ticket.Version,
			}
			handler := NewTicketHandler(service)
			router := humanTicketTestRouter(agent, models.ProjectRoleAgent, func(router *gin.Engine) {
				router.PUT("/tickets/:id", handler.UpdateTicket)
			})
			request := httptest.NewRequest(
				http.MethodPut,
				"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10),
				bytes.NewBufferString(`{"priority":"high"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			if test.ifMatch != "" {
				request.Header.Set("If-Match", test.ifMatch)
			}

			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			assertHumanIfMatchResponse(
				t,
				response,
				test,
				service.calls,
				ticket.Version+1,
			)
		})
	}
}

func TestHumanTicketUpdateRequiresWorkflowAndPreservesTrustedSource(t *testing.T) {
	for _, test := range []struct {
		name          string
		body          string
		currentSource models.TicketSource
		wantCode      string
	}{
		{
			name:          "status uses workflow endpoint",
			body:          `{"status":"cancelled"}`,
			currentSource: models.TicketSourceWeb,
			wantCode:      "workflow_transition_required",
		},
		{
			name:          "null status still uses workflow endpoint",
			body:          `{"status":null}`,
			currentSource: models.TicketSourceWeb,
			wantCode:      "workflow_transition_required",
		},
		{
			name:          "human cannot claim agent source",
			body:          `{"source":"agent"}`,
			currentSource: models.TicketSourceWeb,
			wantCode:      "trusted_source_not_human_writable",
		},
		{
			name:          "human cannot rewrite existing agent source",
			body:          `{"source":"web"}`,
			currentSource: models.TicketSourceAgent,
			wantCode:      "trusted_source_not_human_writable",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			workflow, db, agent, _, ticket, _ := setupWorkflowHandlerTest(t)
			if err := db.Model(&models.Ticket{}).
				Where("id = ?", ticket.ID).
				Update("source", test.currentSource).Error; err != nil {
				t.Fatalf("set current source: %v", err)
			}
			service := &recordingVersionedTicketService{
				TicketServiceInterface: workflow.ticketService,
				currentVersion:         ticket.Version,
			}
			handler := NewTicketHandler(service)
			router := humanTicketTestRouter(
				agent,
				models.ProjectRoleAgent,
				func(router *gin.Engine) {
					router.PUT("/tickets/:id", handler.UpdateTicket)
				},
			)
			request := httptest.NewRequest(
				http.MethodPut,
				"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10),
				bytes.NewBufferString(test.body),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("If-Match", httpcontract.FormatETag(ticket.Version))
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			assertHumanProblem(
				t,
				response,
				http.StatusUnprocessableEntity,
				test.wantCode,
			)
			if service.calls != 0 {
				t.Fatalf("forbidden generic update reached service %d time(s)", service.calls)
			}
		})
	}
}

func TestHumanTicketUpdateRejectsNullSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	workflow, _, agent, _, ticket, _ := setupWorkflowHandlerTest(t)
	service := &recordingVersionedTicketService{
		TicketServiceInterface: workflow.ticketService,
		currentVersion:         ticket.Version,
	}
	handler := NewTicketHandler(service)
	router := humanTicketTestRouter(
		agent,
		models.ProjectRoleAgent,
		func(router *gin.Engine) {
			router.PUT("/tickets/:id", handler.UpdateTicket)
		},
	)
	request := httptest.NewRequest(
		http.MethodPut,
		"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10),
		bytes.NewBufferString(`{"source":null}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", httpcontract.FormatETag(ticket.Version))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"null source status=%d, want %d; body=%s",
			response.Code,
			http.StatusBadRequest,
			response.Body.String(),
		)
	}
	if service.calls != 0 {
		t.Fatalf("null source reached service %d time(s)", service.calls)
	}
}

func TestHumanTicketUpdateRejectsNonCanonicalFieldNames(t *testing.T) {
	for _, body := range []string{
		`{"Status":"cancelled"}`,
		`{"SOURCE":"web"}`,
	} {
		body := body
		t.Run(body, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			workflow, _, agent, _, ticket, _ := setupWorkflowHandlerTest(t)
			service := &recordingVersionedTicketService{
				TicketServiceInterface: workflow.ticketService,
				currentVersion:         ticket.Version,
			}
			handler := NewTicketHandler(service)
			router := humanTicketTestRouter(
				agent,
				models.ProjectRoleAgent,
				func(router *gin.Engine) {
					router.PUT("/tickets/:id", handler.UpdateTicket)
				},
			)
			request := httptest.NewRequest(
				http.MethodPut,
				"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10),
				bytes.NewBufferString(body),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("If-Match", httpcontract.FormatETag(ticket.Version))
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"non-canonical field status=%d, want %d; body=%s",
					response.Code,
					http.StatusBadRequest,
					response.Body.String(),
				)
			}
			if service.calls != 0 {
				t.Fatalf(
					"non-canonical field reached service %d time(s)",
					service.calls,
				)
			}
		})
	}
}

func TestHumanTicketUpdateAllowsOtherFieldsForTrustedAgentSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	workflow, db, agent, _, ticket, _ := setupWorkflowHandlerTest(t)
	if err := db.Model(&models.Ticket{}).
		Where("id = ?", ticket.ID).
		Update("source", models.TicketSourceAgent).Error; err != nil {
		t.Fatalf("set current source: %v", err)
	}
	service := &recordingVersionedTicketService{
		TicketServiceInterface: workflow.ticketService,
		currentVersion:         ticket.Version,
	}
	handler := NewTicketHandler(service)
	router := humanTicketTestRouter(
		agent,
		models.ProjectRoleAgent,
		func(router *gin.Engine) {
			router.PUT("/tickets/:id", handler.UpdateTicket)
		},
	)
	request := httptest.NewRequest(
		http.MethodPut,
		"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10),
		bytes.NewBufferString(`{"title":"只修改标题"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", httpcontract.FormatETag(ticket.Version))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"trusted-source metadata-only update status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	if service.calls != 1 || service.request == nil {
		t.Fatalf(
			"trusted-source metadata-only update calls=%d request=%+v",
			service.calls,
			service.request,
		)
	}
	if service.request.Source != nil || service.request.Status != nil {
		t.Fatalf(
			"trusted-source metadata-only update carried protected fields: %+v",
			service.request,
		)
	}
	if service.request.Title == nil || *service.request.Title != "只修改标题" {
		t.Fatalf("trusted-source metadata-only update request=%+v", service.request)
	}
}

func TestHumanWorkflowCommandsEnforceIfMatch(t *testing.T) {
	actions := []struct {
		name     string
		path     string
		payload  func(models.User) string
		register func(*gin.Engine, *TicketWorkflowHandler)
	}{
		{
			name: "assign",
			path: "/assign",
			payload: func(other models.User) string {
				return `{"assigned_to_id":` + strconv.FormatUint(uint64(other.ID), 10) + `}`
			},
			register: func(router *gin.Engine, handler *TicketWorkflowHandler) {
				router.POST("/tickets/:id/assign", handler.AssignTicket)
			},
		},
		{
			name: "transfer",
			path: "/transfer",
			payload: func(other models.User) string {
				return `{"assigned_to_id":` + strconv.FormatUint(uint64(other.ID), 10) + `}`
			},
			register: func(router *gin.Engine, handler *TicketWorkflowHandler) {
				router.POST("/tickets/:id/transfer", handler.TransferTicket)
			},
		},
		{
			name: "escalate",
			path: "/escalate",
			payload: func(other models.User) string {
				return `{"reason":"需要升级","escalate_to_id":` +
					strconv.FormatUint(uint64(other.ID), 10) + `}`
			},
			register: func(router *gin.Engine, handler *TicketWorkflowHandler) {
				router.POST("/tickets/:id/escalate", handler.EscalateTicket)
			},
		},
		{
			name: "status",
			path: "/status",
			payload: func(_ models.User) string {
				return `{"status":"in_progress"}`
			},
			register: func(router *gin.Engine, handler *TicketWorkflowHandler) {
				router.POST("/tickets/:id/status", handler.UpdateTicketStatus)
			},
		},
	}

	for _, action := range actions {
		action := action
		for _, precondition := range humanIfMatchCases() {
			precondition := precondition
			t.Run(action.name+"/"+precondition.name, func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				workflow, _, agent, other, ticket, _ := setupWorkflowHandlerTest(t)
				service := &recordingVersionedTicketService{
					TicketServiceInterface: workflow.ticketService,
					currentVersion:         ticket.Version,
				}
				handler := NewTicketWorkflowHandler(service)
				router := humanTicketTestRouter(agent, models.ProjectRoleAgent, func(router *gin.Engine) {
					action.register(router, handler)
				})
				request := httptest.NewRequest(
					http.MethodPost,
					"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10)+action.path,
					bytes.NewBufferString(action.payload(other)),
				)
				request.Header.Set("Content-Type", "application/json")
				if precondition.ifMatch != "" {
					request.Header.Set("If-Match", precondition.ifMatch)
				}

				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)

				assertHumanIfMatchResponse(
					t,
					response,
					precondition,
					service.calls,
					ticket.Version+1,
				)
			})
		}
	}
}

func TestHumanTicketCreateTrimsRequiredTextAndRejectsBlankValues(t *testing.T) {
	for _, test := range []struct {
		name       string
		body       string
		wantStatus int
		wantTitle  string
		wantBody   string
	}{
		{
			name:       "trim",
			body:       `{"title":"  标题  ","description":"  描述  ","type":"request","priority":"normal","source":"web","request_type_version_id":"00000000-0000-7000-8000-000000000102"}`,
			wantStatus: http.StatusCreated,
			wantTitle:  "标题",
			wantBody:   "描述",
		},
		{
			name:       "blank title",
			body:       `{"title":" \t\n ","description":"描述","type":"request","priority":"normal","source":"web","request_type_version_id":"00000000-0000-7000-8000-000000000102"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "blank description",
			body:       `{"title":"标题","description":" \t\n ","type":"request","priority":"normal","source":"web","request_type_version_id":"00000000-0000-7000-8000-000000000102"}`,
			wantStatus: http.StatusBadRequest,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			service := &recordingCreateTicketService{}
			handler := NewTicketHandler(service)
			user := models.User{ID: 7, PlatformRole: models.PlatformRolePlatformAdmin}
			router := humanTicketTestRouter(user, models.ProjectRoleAdmin, func(router *gin.Engine) {
				router.POST("/tickets", handler.CreateTicket)
			})
			request := httptest.NewRequest(
				http.MethodPost,
				"/tickets",
				bytes.NewBufferString(test.body),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
			}
			if test.wantStatus == http.StatusCreated {
				if service.request == nil ||
					service.request.Title != test.wantTitle ||
					service.request.Description != test.wantBody {
					t.Fatalf("trimmed request=%+v, want title=%q description=%q", service.request, test.wantTitle, test.wantBody)
				}
				if response.Header().Get("ETag") != httpcontract.FormatETag(1) {
					t.Fatalf("created ETag=%q", response.Header().Get("ETag"))
				}
				return
			}
			if service.request != nil {
				t.Fatalf("blank request reached service: %+v", service.request)
			}
			assertHumanProblem(t, response, http.StatusBadRequest, "invalid_request")
		})
	}
}

func TestHumanTicketCreateRejectsLifecycleStateAndTrustedAgentSource(t *testing.T) {
	for _, test := range []struct {
		name     string
		body     string
		wantCode string
	}{
		{
			name: "status is server assigned from workflow",
			body: `{
				"title":"标题",
				"description":"描述",
				"type":"request",
				"priority":"normal",
				"status":"open",
				"source":"web",
				"request_type_version_id":"00000000-0000-7000-8000-000000000102"
			}`,
			wantCode: "workflow_transition_required",
		},
		{
			name: "null status is still unpublished",
			body: `{
				"title":"标题",
				"description":"描述",
				"type":"request",
				"priority":"normal",
				"status":null,
				"source":"web",
				"request_type_version_id":"00000000-0000-7000-8000-000000000102"
			}`,
			wantCode: "workflow_transition_required",
		},
		{
			name: "agent source is machine assigned",
			body: `{
				"title":"标题",
				"description":"描述",
				"type":"request",
				"priority":"normal",
				"source":"agent",
				"request_type_version_id":"00000000-0000-7000-8000-000000000102"
			}`,
			wantCode: "trusted_source_not_human_writable",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			service := &recordingCreateTicketService{}
			handler := NewTicketHandler(service)
			user := models.User{
				ID:           7,
				PlatformRole: models.PlatformRolePlatformAdmin,
			}
			router := humanTicketTestRouter(
				user,
				models.ProjectRoleAdmin,
				func(router *gin.Engine) {
					router.POST("/tickets", handler.CreateTicket)
				},
			)
			request := httptest.NewRequest(
				http.MethodPost,
				"/tickets",
				bytes.NewBufferString(test.body),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			assertHumanProblem(
				t,
				response,
				http.StatusUnprocessableEntity,
				test.wantCode,
			)
			if service.request != nil {
				t.Fatalf("forbidden create reached service: %+v", service.request)
			}
		})
	}
}

func TestHumanTicketCreateRejectsNonCanonicalStatusField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &recordingCreateTicketService{}
	handler := NewTicketHandler(service)
	user := models.User{
		ID:           7,
		PlatformRole: models.PlatformRolePlatformAdmin,
	}
	router := humanTicketTestRouter(
		user,
		models.ProjectRoleAdmin,
		func(router *gin.Engine) {
			router.POST("/tickets", handler.CreateTicket)
		},
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/tickets",
		bytes.NewBufferString(`{
			"title":"标题",
			"description":"描述",
			"type":"request",
			"priority":"normal",
			"Status":"cancelled",
			"source":"web",
			"request_type_version_id":"00000000-0000-7000-8000-000000000102"
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"non-canonical status field=%d, want %d; body=%s",
			response.Code,
			http.StatusBadRequest,
			response.Body.String(),
		)
	}
	if service.request != nil {
		t.Fatalf("non-canonical create reached service: %+v", service.request)
	}
}

func TestHumanTicketWriteRejectsOversizedBodyBeforeService(t *testing.T) {
	oversizedBody := strings.Repeat(
		" ",
		int(humanTicketRequestBodyLimit)+1,
	)

	t.Run("create with declared content length", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		service := &recordingCreateTicketService{}
		handler := NewTicketHandler(service)
		user := models.User{
			ID:           7,
			PlatformRole: models.PlatformRolePlatformAdmin,
		}
		router := humanTicketTestRouter(
			user,
			models.ProjectRoleAdmin,
			func(router *gin.Engine) {
				router.POST("/tickets", handler.CreateTicket)
			},
		)
		request := httptest.NewRequest(
			http.MethodPost,
			"/tickets",
			strings.NewReader(oversizedBody),
		)
		response := httptest.NewRecorder()

		router.ServeHTTP(response, request)

		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf(
				"oversized create=%d, want %d; body=%s",
				response.Code,
				http.StatusRequestEntityTooLarge,
				response.Body.String(),
			)
		}
		if service.request != nil {
			t.Fatalf("oversized create reached service: %+v", service.request)
		}
	})

	t.Run("update with streamed content length", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		workflow, _, agent, _, ticket, _ := setupWorkflowHandlerTest(t)
		service := &recordingVersionedTicketService{
			TicketServiceInterface: workflow.ticketService,
			currentVersion:         ticket.Version,
		}
		handler := NewTicketHandler(service)
		router := humanTicketTestRouter(
			agent,
			models.ProjectRoleAgent,
			func(router *gin.Engine) {
				router.PUT("/tickets/:id", handler.UpdateTicket)
			},
		)
		request := httptest.NewRequest(
			http.MethodPut,
			"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10),
			strings.NewReader(oversizedBody),
		)
		request.ContentLength = -1
		request.Header.Set("If-Match", httpcontract.FormatETag(ticket.Version))
		response := httptest.NewRecorder()

		router.ServeHTTP(response, request)

		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf(
				"oversized update=%d, want %d; body=%s",
				response.Code,
				http.StatusRequestEntityTooLarge,
				response.Body.String(),
			)
		}
		if service.calls != 0 {
			t.Fatalf(
				"oversized update reached service %d time(s)",
				service.calls,
			)
		}
	})
}

func TestHumanTicketCreateMapsDomainMembershipDenialToForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &recordingCreateTicketService{
		err: services.ErrTicketCreateAccessDenied,
	}
	handler := NewTicketHandler(service)
	user := models.User{ID: 7, PlatformRole: models.PlatformRolePlatformAdmin}
	router := humanTicketTestRouter(user, models.ProjectRoleAdmin, func(router *gin.Engine) {
		router.POST("/tickets", handler.CreateTicket)
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/tickets",
		bytes.NewBufferString(
			`{"title":"标题","description":"描述","type":"request","priority":"normal","source":"web","request_type_version_id":"00000000-0000-7000-8000-000000000102"}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assertHumanProblem(
		t,
		response,
		http.StatusForbidden,
		"ticket_create_access_denied",
	)
}

func TestCustomerHistoryRedactsInternalAndAssignmentRecords(t *testing.T) {
	humanID := uint(7)
	histories := []*models.TicketHistory{
		{
			ID:          1,
			TicketID:    9,
			ActorType:   models.ActorTypeHuman,
			ActorID:     "7",
			Action:      models.HistoryActionCreate,
			Description: "工单已创建",
			IsVisible:   true,
		},
		{
			ID:          2,
			TicketID:    9,
			UserID:      &humanID,
			ActorType:   models.ActorTypeHuman,
			ActorID:     "7",
			Action:      models.HistoryActionAssign,
			Description: "工单已分配给用户 ID: 11",
			FieldName:   "assigned_to_id",
			NewValue:    "11",
			IsVisible:   true,
		},
		{
			ID:          3,
			TicketID:    9,
			ActorType:   models.ActorTypeHuman,
			ActorID:     "7",
			Action:      models.HistoryActionUpdate,
			Description: "内部备注已更新",
			FieldName:   "internal_notes",
			NewValue:    "仅内部可见",
			IsVisible:   true,
		},
		{
			ID:          4,
			TicketID:    9,
			ActorType:   models.ActorTypeServicePrincipal,
			ActorID:     "principal-1",
			Action:      models.HistoryActionComment,
			Description: "添加了评论",
			IsVisible:   false,
		},
	}

	customer := ticketHistoryResponses(histories, true)
	if len(customer) != 1 || customer[0].ID != 1 {
		t.Fatalf("customer history=%+v, want only public create record", customer)
	}
	if customer[0].Actor != nil {
		t.Fatalf("customer history exposed actor: %+v", customer[0].Actor)
	}

	privileged := ticketHistoryResponses(histories, false)
	if len(privileged) != len(histories) {
		t.Fatalf("privileged history count=%d, want %d", len(privileged), len(histories))
	}
	for i := range privileged {
		if privileged[i].Actor == nil {
			t.Fatalf("privileged history %d lost actor", privileged[i].ID)
		}
	}
}

type humanIfMatchCase struct {
	name       string
	ifMatch    string
	wantStatus int
	wantCode   string
	wantCalls  int
}

func humanIfMatchCases() []humanIfMatchCase {
	return []humanIfMatchCase{
		{
			name:       "missing",
			wantStatus: http.StatusPreconditionRequired,
			wantCode:   "precondition_required",
		},
		{
			name:       "malformed",
			ifMatch:    "not-an-etag",
			wantStatus: http.StatusConflict,
			wantCode:   "version_conflict",
		},
		{
			name:       "stale",
			ifMatch:    httpcontract.FormatETag(2),
			wantStatus: http.StatusConflict,
			wantCode:   "version_conflict",
			wantCalls:  1,
		},
		{
			name:       "current",
			ifMatch:    httpcontract.FormatETag(1),
			wantStatus: http.StatusOK,
			wantCalls:  1,
		},
	}
}

func assertHumanIfMatchResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	test humanIfMatchCase,
	gotCalls int,
	successVersion uint64,
) {
	t.Helper()
	if response.Code != test.wantStatus {
		t.Fatalf("%s status=%d, want %d; body=%s", test.name, response.Code, test.wantStatus, response.Body.String())
	}
	if gotCalls != test.wantCalls {
		t.Fatalf("%s versioned service calls=%d, want %d", test.name, gotCalls, test.wantCalls)
	}
	if test.wantStatus == http.StatusOK {
		if got, want := response.Header().Get("ETag"), httpcontract.FormatETag(successVersion); got != want {
			t.Fatalf("%s ETag=%q, want %q", test.name, got, want)
		}
		return
	}
	assertHumanProblem(t, response, test.wantStatus, test.wantCode)
}

func assertHumanProblem(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	code string,
) {
	t.Helper()
	if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("problem Content-Type=%q", got)
	}
	var problem humanTicketProblem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v; body=%s", err, response.Body.String())
	}
	if problem.Status != status || problem.Code != code {
		t.Fatalf("problem=%+v, want status=%d code=%q", problem, status, code)
	}
	if problem.Title == "" || problem.Detail == "" {
		t.Fatalf("problem lacks Chinese operator feedback: %+v", problem)
	}
}

func humanTicketTestRouter(
	user models.User,
	projectRole models.ProjectRole,
	register func(*gin.Engine),
) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("platform_role", user.PlatformRole)
		c.Set(projectRoleContextKey, string(projectRole))
		requestContext, err := services.WithOperationContext(
			c.Request.Context(),
			services.OperationContext{
				Scope: models.ProjectScope{
					OrganizationID: 1,
					ProjectID:      1,
				},
				Actor:  models.HumanActor(user.ID),
				Source: services.SourceProtocolHumanREST,
			},
		)
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Request = c.Request.WithContext(requestContext)
		c.Next()
	})
	register(router)
	return router
}

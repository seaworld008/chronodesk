package services

import (
	"errors"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
)

func TestValidateTicketRequestFormUsesPublishedSchemaAndRejectsReservedFields(
	t *testing.T,
) {
	t.Parallel()

	requestType := models.RequestTypeVersion{
		JSONSchema: datatypes.JSON(`{
			"$schema":"https://json-schema.org/draft/2020-12/schema",
			"type":"object",
			"properties":{
				"summary":{"type":"string","minLength":1},
				"description":{"type":"string","minLength":1},
				"priority":{"type":"string","enum":["low","normal","high","urgent","critical"]},
				"risk_level":{"type":"string","enum":["low","medium","high"]}
			},
			"required":["summary","description","priority","risk_level"],
			"additionalProperties":false
		}`),
	}
	baseRequest := func() *models.TicketCreateRequest {
		customFields := models.JSONMap{"risk_level": "medium"}
		return &models.TicketCreateRequest{
			Title:        "数据库连接异常",
			Description:  "生产环境连接池持续耗尽。",
			Type:         models.TicketTypeIncident,
			Priority:     models.TicketPriorityNormal,
			Source:       models.TicketSourceWeb,
			CustomFields: &customFields,
		}
	}

	tests := []struct {
		name    string
		mutate  func(*models.TicketCreateRequest)
		wantErr bool
	}{
		{name: "valid derived core and custom fields"},
		{
			name: "missing required custom field",
			mutate: func(request *models.TicketCreateRequest) {
				empty := models.JSONMap{}
				request.CustomFields = &empty
			},
			wantErr: true,
		},
		{
			name: "reserved project field cannot hide in custom fields",
			mutate: func(request *models.TicketCreateRequest) {
				fields := models.JSONMap{
					"risk_level": "medium",
					"project_id": 999,
				}
				request.CustomFields = &fields
			},
			wantErr: true,
		},
		{
			name: "schema enum remains authoritative",
			mutate: func(request *models.TicketCreateRequest) {
				fields := models.JSONMap{"risk_level": "unbounded"}
				request.CustomFields = &fields
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := baseRequest()
			if test.mutate != nil {
				test.mutate(request)
			}
			err := validateTicketRequestForm(requestType, request)
			if test.wantErr && !errors.Is(err, ErrTicketFormValidation) {
				t.Fatalf("validateTicketRequestForm() error = %v, want form validation", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("validateTicketRequestForm() unexpected error: %v", err)
			}
		})
	}
}

func TestTicketTransitionUsesStoredWorkflowVersionInsteadOfHardcodedLifecycle(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "workflow-runtime")
	native := NewAgentNativeService(db)
	ticketService, err := NewTicketService(
		db,
		native,
		nil,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := testProjectOperationContext(t, db, models.HumanActor(user.ID))
	ticket, err := ticketService.CreateTicket(
		ctx,
		&models.TicketCreateRequest{
			Title:       "验证版本化工作流",
			Description: "状态流转必须服从工单绑定的不可变工作流版本。",
			Type:        models.TicketTypeRequest,
			Priority:    models.TicketPriorityNormal,
			Source:      models.TicketSourceWeb,
		},
		user.ID,
	)
	if err != nil {
		t.Fatal(err)
	}

	// The former hardcoded lifecycle allowed open -> resolved, while the
	// published bootstrap workflow intentionally requires start -> resolve.
	if _, err := ticketService.UpdateTicketStatusExpectedVersion(
		ctx,
		ticket.ID,
		string(models.TicketStatusResolved),
		user.ID,
		"",
		"",
		ticket.Version,
	); !errors.Is(err, ErrInvalidTicketTransition) {
		t.Fatalf("open -> resolved error = %v, want workflow rejection", err)
	}

	started, err := ticketService.UpdateTicketStatusExpectedVersion(
		ctx,
		ticket.ID,
		string(models.TicketStatusInProgress),
		user.ID,
		"",
		"",
		ticket.Version,
	)
	if err != nil {
		t.Fatalf("published workflow start transition failed: %v", err)
	}
	if started.Status != models.TicketStatusInProgress ||
		started.WorkflowVersionID != ticket.WorkflowVersionID {
		t.Fatalf("unexpected workflow transition result: %+v", started)
	}
}

func TestValidateTicketRequestFormForbidsExternalSchemaReferences(t *testing.T) {
	t.Parallel()
	requestType := models.RequestTypeVersion{
		JSONSchema: datatypes.JSON(`{
			"$schema":"https://json-schema.org/draft/2020-12/schema",
			"$ref":"https://attacker.invalid/schema.json"
		}`),
	}
	request := &models.TicketCreateRequest{
		Title:       "外部引用",
		Description: "不得从网络加载。",
		Type:        models.TicketTypeRequest,
		Priority:    models.TicketPriorityNormal,
		Source:      models.TicketSourceWeb,
	}
	if err := validateTicketRequestForm(requestType, request); !errors.Is(
		err,
		ErrTicketFormValidation,
	) {
		t.Fatalf("validateTicketRequestForm() error = %v, want form validation", err)
	}
}

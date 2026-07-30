package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
)

var errTicketAccessDenied = errors.New("ticket access denied")

type humanTicketProblem struct {
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Status    int            `json:"status"`
	Detail    string         `json:"detail"`
	Code      string         `json:"code"`
	RequestID string         `json:"request_id,omitempty"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type ticketAccessMode int

const (
	ticketAccessRead ticketAccessMode = iota
	ticketAccessUpdate
	ticketAccessWorkflow
	ticketAccessDelete
)

func normalizedProjectRole(c *gin.Context) string {
	return strings.ToLower(strings.TrimSpace(c.GetString(projectRoleContextKey)))
}

func isRequesterRole(role string) bool {
	return role == string(models.ProjectRoleRequester)
}

func isProjectManagerRole(role string) bool {
	return role == string(models.ProjectRoleAdmin) ||
		role == string(models.ProjectRoleManager)
}

func isProjectAgentRole(role string) bool {
	return role == string(models.ProjectRoleAgent)
}

func isProjectObserverRole(role string) bool {
	return role == string(models.ProjectRoleObserver)
}

func authorizeTicket(
	ctx context.Context,
	c *gin.Context,
	service services.TicketServiceInterface,
	ticketID uint,
	mode ticketAccessMode,
) (*models.Ticket, error) {
	ticket, err := service.GetTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}

	role := normalizedProjectRole(c)
	userID := c.GetUint("user_id")
	if isProjectManagerRole(role) {
		return ticket, nil
	}
	if mode == ticketAccessDelete {
		return nil, errTicketAccessDenied
	}

	if isRequesterRole(role) {
		if ticket.CreatedByID == nil || *ticket.CreatedByID != userID {
			return nil, errTicketAccessDenied
		}
		if mode == ticketAccessWorkflow {
			return nil, errTicketAccessDenied
		}
		return ticket, nil
	}

	if isProjectAgentRole(role) {
		if mode == ticketAccessRead {
			return ticket, nil
		}
		if ticket.AssignedToID == nil || *ticket.AssignedToID == userID {
			return ticket, nil
		}
		return nil, errTicketAccessDenied
	}
	if isProjectObserverRole(role) && mode == ticketAccessRead {
		return ticket, nil
	}

	return nil, errTicketAccessDenied
}

func writeTicketAuthorizationError(c *gin.Context, err error) bool {
	if !errors.Is(err, errTicketAccessDenied) {
		return false
	}
	c.JSON(403, gin.H{
		"code": "ticket_access_denied",
		"msg":  "无权访问或修改该工单",
	})
	return true
}

func writeHumanTicketProblem(
	c *gin.Context,
	status int,
	code string,
	title string,
	detail string,
	retryable bool,
	details ...map[string]any,
) {
	requestID := c.GetString("request_id")
	if requestID == "" {
		requestID = strings.TrimSpace(c.GetHeader("X-Request-ID"))
	}
	c.Header("Content-Type", "application/problem+json")
	problem := humanTicketProblem{
		Type:      "https://chronodesk.local/problems/" + code,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Code:      code,
		RequestID: requestID,
		Retryable: retryable,
	}
	if len(details) > 0 {
		problem.Details = details[0]
	}
	c.JSON(status, problem)
}

func requireTicketIfMatch(c *gin.Context) (uint64, bool) {
	expectedVersion, err := httpcontract.ParseIfMatch(c.GetHeader("If-Match"))
	switch {
	case errors.Is(err, httpcontract.ErrIfMatchRequired):
		writeHumanTicketProblem(
			c,
			http.StatusPreconditionRequired,
			"precondition_required",
			"缺少请求前置条件",
			"必须提供当前工单版本对应的 If-Match 请求头",
			false,
		)
		return 0, false
	case err != nil:
		writeHumanTicketProblem(
			c,
			http.StatusConflict,
			"version_conflict",
			"工单版本冲突",
			`If-Match 必须使用强 ETag 格式，例如 "v1"`,
			false,
		)
		return 0, false
	default:
		return expectedVersion, true
	}
}

func writeTicketVersionConflict(c *gin.Context) {
	writeHumanTicketProblem(
		c,
		http.StatusConflict,
		"version_conflict",
		"工单版本冲突",
		"工单已被其他操作更新，请重新读取后再试",
		true,
	)
}

func setTicketETag(c *gin.Context, version uint64) {
	c.Header("ETag", httpcontract.FormatETag(version))
}

// ticketResponseForRole prevents customer-facing collection and detail endpoints from disclosing
// another user's contact, authentication, role, or login metadata.
func ticketResponseForRole(ticket *models.Ticket, role string) *models.TicketResponse {
	if ticket == nil {
		return nil
	}
	response := ticket.ToResponse()
	if isRequesterRole(role) {
		response.CreatedBy = nil
		response.AssignedToID = nil
		response.AssignedTo = nil
		response.AssignedToActor = nil
		response.AgentContext = models.AgentContext{}
	}
	return response
}

func ticketListResponseForRole(tickets []*models.Ticket, role string) []*models.TicketResponse {
	result := make([]*models.TicketResponse, 0, len(tickets))
	for _, ticket := range tickets {
		if ticket != nil {
			result = append(result, ticketResponseForRole(ticket, role))
		}
	}
	return result
}

// Ticket history deliberately has its own narrow DTO. Raw TicketHistory rows
// contain request metadata (IP address, user agent and arbitrary JSON) that
// must never cross the HTTP boundary.
type ticketHistoryResponse struct {
	ID              uint                           `json:"id"`
	CreatedAt       time.Time                      `json:"created_at"`
	TicketID        uint                           `json:"ticket_id"`
	Actor           *models.ActorRef               `json:"actor,omitempty"`
	EventID         *string                        `json:"event_id,omitempty"`
	ResourceVersion uint64                         `json:"resource_version,omitempty"`
	Provenance      models.TicketHistoryProvenance `json:"provenance,omitempty"`
	Action          models.HistoryAction           `json:"action"`
	Description     string                         `json:"description"`
	FieldName       string                         `json:"field_name,omitempty"`
	OldValue        string                         `json:"old_value,omitempty"`
	NewValue        string                         `json:"new_value,omitempty"`
	CommentID       *uint                          `json:"comment_id,omitempty"`
	AttachmentID    *uint                          `json:"attachment_id,omitempty"`
	Duration        *int                           `json:"duration,omitempty"`
	ScheduledAt     *time.Time                     `json:"scheduled_at,omitempty"`
	CompletedAt     *time.Time                     `json:"completed_at,omitempty"`
	IsVisible       bool                           `json:"is_visible"`
	IsSystem        bool                           `json:"is_system"`
	IsAutomated     bool                           `json:"is_automated"`
	IsImportant     bool                           `json:"is_important"`
}

func ticketHistoryResponses(histories []*models.TicketHistory, customer bool) []*ticketHistoryResponse {
	result := make([]*ticketHistoryResponse, 0, len(histories))
	for _, history := range histories {
		if history == nil || (customer && !customerCanSeeTicketHistory(history)) {
			continue
		}
		response := &ticketHistoryResponse{
			ID:           history.ID,
			CreatedAt:    history.CreatedAt,
			TicketID:     history.TicketID,
			Action:       history.Action,
			Description:  history.Description,
			FieldName:    history.FieldName,
			OldValue:     history.OldValue,
			NewValue:     history.NewValue,
			CommentID:    history.CommentID,
			AttachmentID: history.AttachmentID,
			Duration:     history.Duration,
			ScheduledAt:  history.ScheduledAt,
			CompletedAt:  history.CompletedAt,
			IsVisible:    history.IsVisible,
			IsSystem:     history.IsSystem,
			IsAutomated:  history.IsAutomated,
			IsImportant:  history.IsImportant,
		}
		if !customer {
			actor := history.Actor()
			response.Actor = &actor
			response.EventID = history.EventID
			response.ResourceVersion = history.ResourceVersion
			response.Provenance = history.Provenance
		}
		result = append(result, response)
	}
	return result
}

func customerCanSeeTicketHistory(history *models.TicketHistory) bool {
	if history == nil || !history.IsVisible {
		return false
	}
	switch history.Action {
	case models.HistoryActionAssign,
		models.HistoryActionUnassign,
		models.HistoryActionTransfer,
		models.HistoryActionEscalate:
		return false
	}
	switch strings.ToLower(strings.TrimSpace(history.FieldName)) {
	case "assigned_to_id",
		"assigned_to_actor_type",
		"assigned_to_actor_id",
		"assigned_to_service_principal_id",
		"internal_notes":
		return false
	}
	return true
}

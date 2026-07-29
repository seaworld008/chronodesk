package handlers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
)

var errTicketAccessDenied = errors.New("ticket access denied")

type ticketAccessMode int

const (
	ticketAccessRead ticketAccessMode = iota
	ticketAccessUpdate
	ticketAccessWorkflow
	ticketAccessDelete
)

func normalizedUserRole(c *gin.Context) string {
	return strings.ToLower(strings.TrimSpace(c.GetString("user_role")))
}

func isCustomerRole(role string) bool {
	return role == "user" || role == "customer"
}

func isPrivilegedRole(role string) bool {
	return role == "admin" || role == "superuser" || role == "supervisor"
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

	role := normalizedUserRole(c)
	userID := c.GetUint("user_id")
	if isPrivilegedRole(role) {
		return ticket, nil
	}
	if mode == ticketAccessDelete {
		return nil, errTicketAccessDenied
	}

	if isCustomerRole(role) {
		if ticket.CreatedByID != userID {
			return nil, errTicketAccessDenied
		}
		if mode == ticketAccessWorkflow {
			return nil, errTicketAccessDenied
		}
		return ticket, nil
	}

	if role == "agent" {
		if mode == ticketAccessRead {
			return ticket, nil
		}
		if ticket.AssignedToID == nil || *ticket.AssignedToID == userID {
			return ticket, nil
		}
		return nil, errTicketAccessDenied
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

// ticketResponseForRole keeps the compatibility TicketResponse contract while
// preventing customer-facing collection and detail endpoints from disclosing
// another user's contact, authentication, role, or login metadata.
func ticketResponseForRole(ticket *models.Ticket, role string) *models.TicketResponse {
	if ticket == nil {
		return nil
	}
	response := ticket.ToResponse()
	if isCustomerRole(role) {
		response.CreatedBy = nil
		response.AssignedToID = nil
		response.AssignedTo = nil
		response.AssignedToActor = nil
		response.Attachments = nil
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
	ID           uint                 `json:"id"`
	CreatedAt    time.Time            `json:"created_at"`
	TicketID     uint                 `json:"ticket_id"`
	Actor        *models.ActorRef     `json:"actor,omitempty"`
	Action       models.HistoryAction `json:"action"`
	Description  string               `json:"description"`
	FieldName    string               `json:"field_name,omitempty"`
	OldValue     string               `json:"old_value,omitempty"`
	NewValue     string               `json:"new_value,omitempty"`
	CommentID    *uint                `json:"comment_id,omitempty"`
	AttachmentID *uint                `json:"attachment_id,omitempty"`
	Duration     *int                 `json:"duration,omitempty"`
	ScheduledAt  *time.Time           `json:"scheduled_at,omitempty"`
	CompletedAt  *time.Time           `json:"completed_at,omitempty"`
	IsVisible    bool                 `json:"is_visible"`
	IsSystem     bool                 `json:"is_system"`
	IsAutomated  bool                 `json:"is_automated"`
	IsImportant  bool                 `json:"is_important"`
}

func ticketHistoryResponses(histories []*models.TicketHistory, customer bool) []*ticketHistoryResponse {
	result := make([]*ticketHistoryResponse, 0, len(histories))
	for _, history := range histories {
		if history == nil || (customer && !history.IsVisible) {
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
		}
		result = append(result, response)
	}
	return result
}

// TicketVersionedService binds the object used for authorization to the
// transaction's compare-and-swap. It is intentionally separate from the
// compatibility interface so existing integrations can migrate without
// weakening production agent routes.
type TicketVersionedService interface {
	UpdateTicketExpectedVersion(context.Context, uint, *models.TicketUpdateRequest, uint, uint64) (*models.Ticket, error)
	AssignTicketExpectedVersion(uint, uint, uint, string, uint64) (*models.Ticket, error)
	TransferTicketExpectedVersion(uint, uint, uint, string, string, uint64) (*models.Ticket, error)
	EscalateTicketExpectedVersion(uint, uint, uint, string, string, uint64) (*models.Ticket, error)
	UpdateTicketStatusExpectedVersion(uint, string, uint, string, string, uint64) (*models.Ticket, error)
}

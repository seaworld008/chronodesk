package services

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

const (
	defaultCrossProjectWorkbenchPageSize = 20
	maxCrossProjectWorkbenchPageSize     = 100
	maxCrossProjectWorkbenchPage         = 100_000
	maxCrossProjectWorkbenchProjects     = 500
)

var (
	ErrCrossProjectWorkbenchAccessDenied = errors.New(
		"cross-project workbench access denied",
	)
	ErrCrossProjectWorkbenchQuery = errors.New(
		"invalid cross-project workbench query",
	)
	ErrCrossProjectWorkbenchProjectLimit = errors.New(
		"cross-project workbench project limit exceeded",
	)
)

// CrossProjectWorkbenchView is a closed set so transport input can never
// become an SQL fragment.
type CrossProjectWorkbenchView string

const (
	CrossProjectWorkbenchTodo     CrossProjectWorkbenchView = "todo"
	CrossProjectWorkbenchCreated  CrossProjectWorkbenchView = "created"
	CrossProjectWorkbenchAssigned CrossProjectWorkbenchView = "assigned"
)

func (view CrossProjectWorkbenchView) IsValid() bool {
	switch view {
	case CrossProjectWorkbenchTodo,
		CrossProjectWorkbenchCreated,
		CrossProjectWorkbenchAssigned:
		return true
	default:
		return false
	}
}

type CrossProjectWorkbenchQuery struct {
	UserID   uint
	View     CrossProjectWorkbenchView
	Page     int
	PageSize int
}

type CrossProjectWorkbenchTicket struct {
	ID             uint                  `json:"id"`
	PublicID       string                `json:"public_id"`
	ProjectID      uint                  `json:"project_id"`
	ProjectKey     models.ProjectKey     `json:"project_key"`
	ProjectName    string                `json:"project_name"`
	TicketNumber   string                `json:"ticket_number"`
	Title          string                `json:"title"`
	Type           models.TicketType     `json:"type"`
	Priority       models.TicketPriority `json:"priority"`
	Status         models.TicketStatus   `json:"status"`
	CreatedByID    *uint                 `json:"created_by_id,omitempty"`
	AssignedToID   *uint                 `json:"assigned_to_id,omitempty"`
	AssignedToName string                `json:"assigned_to_name,omitempty"`
	DueDate        *time.Time            `json:"due_date,omitempty"`
	SLADueDate     *time.Time            `json:"sla_due_date,omitempty"`
	SLABreached    bool                  `json:"sla_breached"`
	Version        uint64                `json:"version"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
}

type CrossProjectWorkbenchPage struct {
	Items      []CrossProjectWorkbenchTicket `json:"items"`
	Total      int64                         `json:"total"`
	Page       int                           `json:"page"`
	PageSize   int                           `json:"page_size"`
	TotalPages int                           `json:"total_pages"`
	View       CrossProjectWorkbenchView     `json:"view"`
}

type authorizedWorkbenchProject struct {
	ID             uint              `gorm:"column:id"`
	OrganizationID uint              `gorm:"column:organization_id"`
	Key            models.ProjectKey `gorm:"column:key"`
	Name           string            `gorm:"column:name"`
}

type CrossProjectWorkbenchService struct {
	db *gorm.DB
}

func NewCrossProjectWorkbenchService(
	db *gorm.DB,
) (*CrossProjectWorkbenchService, error) {
	if db == nil {
		return nil, errors.New("cross-project workbench database is required")
	}
	return &CrossProjectWorkbenchService{db: db}, nil
}

// ListTickets returns a human's explicitly authorized cross-project view.
// Platform roles deliberately are not accepted by this method: the ordinary
// workbench is always restricted by active ProjectMembership rows.
func (service *CrossProjectWorkbenchService) ListTickets(
	ctx context.Context,
	input CrossProjectWorkbenchQuery,
) (*CrossProjectWorkbenchPage, error) {
	if ctx == nil || input.UserID == 0 {
		return nil, ErrCrossProjectWorkbenchAccessDenied
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("cross-project workbench context: %w", err)
	}

	normalized, err := normalizeCrossProjectWorkbenchQuery(input)
	if err != nil {
		return nil, err
	}
	projects, err := service.authorizedProjects(ctx, normalized.UserID)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return emptyCrossProjectWorkbenchPage(normalized), nil
	}

	projectIDs := make([]uint, 0, len(projects))
	organizationID := projects[0].OrganizationID
	if organizationID == 0 {
		return nil, ErrCrossProjectWorkbenchAccessDenied
	}
	for _, project := range projects {
		if project.ID == 0 || project.OrganizationID != organizationID {
			return nil, ErrCrossProjectWorkbenchAccessDenied
		}
		projectIDs = append(projectIDs, project.ID)
	}
	actorID := strconv.FormatUint(uint64(normalized.UserID), 10)
	var total int64
	items := make([]CrossProjectWorkbenchTicket, 0, normalized.PageSize)
	offset := (normalized.Page - 1) * normalized.PageSize
	if err := scopeddb.WithAuthorizedProjectScopeTransaction(
		ctx,
		service.db,
		organizationID,
		projectIDs,
		func(scopedCtx context.Context) error {
			query := service.db.WithContext(scopedCtx).
				Table("tickets AS tickets").
				Joins(
					"JOIN projects AS projects ON projects.id = tickets.project_id",
				).
				Joins(
					"LEFT JOIN users AS assignees ON assignees.id = tickets.assigned_to_id",
				).
				Where("tickets.deleted_at IS NULL").
				// The explicit authorized-ID predicates remain mandatory even
				// though PostgreSQL RLS independently enforces the same set.
				Where("tickets.organization_id = ?", organizationID).
				Where("tickets.project_id IN ?", projectIDs).
				Where("projects.organization_id = ?", organizationID).
				Where("projects.id IN ?", projectIDs).
				Where("projects.status = ?", models.ProjectStatusActive)
			query = applyCrossProjectWorkbenchView(
				query,
				normalized.View,
				actorID,
			)

			if err := query.Count(&total).Error; err != nil {
				return fmt.Errorf(
					"count cross-project workbench tickets: %w",
					err,
				)
			}
			if err := query.
				Select(strings.Join([]string{
					"tickets.id",
					"tickets.public_id",
					"tickets.project_id",
					"projects.key AS project_key",
					"projects.name AS project_name",
					"tickets.ticket_number",
					"tickets.title",
					"tickets.type",
					"tickets.priority",
					"tickets.status",
					"tickets.created_by_id",
					"tickets.assigned_to_id",
					"COALESCE(NULLIF(assignees.display_name, ''), assignees.username, '') AS assigned_to_name",
					"tickets.due_date",
					"tickets.sla_due_date",
					"tickets.sla_breached",
					"tickets.version",
					"tickets.created_at",
					"tickets.updated_at",
				}, ", ")).
				Order("tickets.updated_at DESC, tickets.id DESC").
				Offset(offset).
				Limit(normalized.PageSize).
				Scan(&items).Error; err != nil {
				return fmt.Errorf(
					"list cross-project workbench tickets: %w",
					err,
				)
			}
			return nil
		},
	); err != nil {
		return nil, err
	}

	return &CrossProjectWorkbenchPage{
		Items:      items,
		Total:      total,
		Page:       normalized.Page,
		PageSize:   normalized.PageSize,
		TotalPages: crossProjectWorkbenchTotalPages(total, normalized.PageSize),
		View:       normalized.View,
	}, nil
}

func (service *CrossProjectWorkbenchService) authorizedProjects(
	ctx context.Context,
	userID uint,
) ([]authorizedWorkbenchProject, error) {
	projects := make([]authorizedWorkbenchProject, 0)
	if err := service.db.WithContext(ctx).
		Table("projects AS projects").
		Select(
			"projects.id, projects.organization_id, projects.key, projects.name",
		).
		Joins(
			"JOIN project_memberships AS memberships ON memberships.project_id = projects.id",
		).
		Where("memberships.user_id = ?", userID).
		Where("memberships.is_active = ?", true).
		Where("projects.status = ?", models.ProjectStatusActive).
		Order("projects.id ASC").
		Limit(maxCrossProjectWorkbenchProjects + 1).
		Scan(&projects).Error; err != nil {
		return nil, fmt.Errorf("list workbench project memberships: %w", err)
	}
	if len(projects) > maxCrossProjectWorkbenchProjects {
		return nil, ErrCrossProjectWorkbenchProjectLimit
	}
	return projects, nil
}

func normalizeCrossProjectWorkbenchQuery(
	input CrossProjectWorkbenchQuery,
) (CrossProjectWorkbenchQuery, error) {
	if input.UserID == 0 {
		return CrossProjectWorkbenchQuery{},
			ErrCrossProjectWorkbenchAccessDenied
	}
	if input.View == "" {
		input.View = CrossProjectWorkbenchTodo
	}
	if !input.View.IsValid() {
		return CrossProjectWorkbenchQuery{}, ErrCrossProjectWorkbenchQuery
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PageSize == 0 {
		input.PageSize = defaultCrossProjectWorkbenchPageSize
	}
	if input.Page < 1 ||
		input.Page > maxCrossProjectWorkbenchPage ||
		input.PageSize < 1 ||
		input.PageSize > maxCrossProjectWorkbenchPageSize {
		return CrossProjectWorkbenchQuery{}, ErrCrossProjectWorkbenchQuery
	}
	return input, nil
}

func applyCrossProjectWorkbenchView(
	query *gorm.DB,
	view CrossProjectWorkbenchView,
	actorID string,
) *gorm.DB {
	switch view {
	case CrossProjectWorkbenchCreated:
		return query.Where(
			"tickets.created_by_actor_type = ? AND tickets.created_by_actor_id = ?",
			models.ActorTypeHuman,
			actorID,
		)
	case CrossProjectWorkbenchAssigned:
		return query.Where(
			"tickets.assigned_to_actor_type = ? AND tickets.assigned_to_actor_id = ?",
			models.ActorTypeHuman,
			actorID,
		)
	default:
		return query.
			Where(
				"tickets.assigned_to_actor_type = ? AND tickets.assigned_to_actor_id = ?",
				models.ActorTypeHuman,
				actorID,
			).
			Where("tickets.status IN ?", []models.TicketStatus{
				models.TicketStatusOpen,
				models.TicketStatusInProgress,
				models.TicketStatusPending,
			})
	}
}

func emptyCrossProjectWorkbenchPage(
	input CrossProjectWorkbenchQuery,
) *CrossProjectWorkbenchPage {
	return &CrossProjectWorkbenchPage{
		Items:      []CrossProjectWorkbenchTicket{},
		Total:      0,
		Page:       input.Page,
		PageSize:   input.PageSize,
		TotalPages: 0,
		View:       input.View,
	}
}

func crossProjectWorkbenchTotalPages(total int64, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	return int((total + int64(pageSize) - 1) / int64(pageSize))
}

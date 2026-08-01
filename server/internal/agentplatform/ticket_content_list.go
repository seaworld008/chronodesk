package agentplatform

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/listcursor"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

const (
	agentTicketContentListDefault = 25
	agentTicketContentListMax     = 100
	agentTicketContentCursorV1    = 1
	agentTicketContentSortV1      = "created_at_desc_id_desc.v1"

	agentTicketCommentsList    = "ticket_comments"
	agentTicketAttachmentsList = "ticket_attachments"
)

var (
	errAgentTicketContentListQuery  = errors.New("ticket content list query is invalid")
	errAgentTicketContentListCursor = errors.New("ticket content list cursor is invalid")
	errAgentTicketContentCursorKey  = errors.New("ticket content cursor signing key is unavailable")
)

type agentTicketContentListCursors struct {
	comments    *listcursor.Codec
	attachments *listcursor.Codec
}

type agentTicketContentCursor struct {
	Version      int    `json:"v"`
	Kind         string `json:"kind"`
	Organization uint   `json:"organization_id"`
	Project      uint   `json:"project_id"`
	Ticket       uint   `json:"ticket_id"`
	Limit        int    `json:"limit"`
	FilterHash   string `json:"filter_hash"`
	SortVersion  string `json:"sort_version"`
	CreatedAt    string `json:"created_at"`
	ID           uint   `json:"id"`
}

type agentTicketContentListQuery struct {
	Limit  int
	Cursor string
	After  *agentTicketContentListPosition
}

type agentTicketContentListPosition struct {
	CreatedAt time.Time
	ID        uint
}

type agentTicketCommentPage struct {
	Items      []*models.TicketCommentResponse `json:"items"`
	NextCursor string                          `json:"next_cursor"`
	HasMore    bool                            `json:"has_more"`
}

type agentTicketAttachmentPage struct {
	Items      []*models.TicketAttachmentResponse `json:"items"`
	NextCursor string                             `json:"next_cursor"`
	HasMore    bool                               `json:"has_more"`
}

// ConfigureTicketContentListCursor derives endpoint-specific signing keys from
// deployment-owned key material. The application must configure it before
// serving Agent REST comment or attachment lists; handlers otherwise fail
// closed instead of emitting reusable unsigned cursors.
func (h *APIHandler) ConfigureTicketContentListCursor(rootKey []byte) error {
	if h == nil || len(rootKey) == 0 {
		return errAgentTicketContentCursorKey
	}
	comments, err := listcursor.NewCodec(
		rootKey,
		"agent-rest-ticket-comments.v1",
	)
	if err != nil {
		return fmt.Errorf("%w: %v", errAgentTicketContentCursorKey, err)
	}
	attachments, err := listcursor.NewCodec(
		rootKey,
		"agent-rest-ticket-attachments.v1",
	)
	if err != nil {
		return fmt.Errorf("%w: %v", errAgentTicketContentCursorKey, err)
	}
	h.ticketContentLists = &agentTicketContentListCursors{
		comments:    comments,
		attachments: attachments,
	}
	return nil
}

func (h *APIHandler) requireTicketContentListQuery(
	c *gin.Context,
	kind string,
	scope models.ProjectScope,
	ticketID uint,
) (agentTicketContentListQuery, bool) {
	query, err := parseAgentTicketContentListQuery(c.Request.URL.RawQuery)
	if err != nil {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"Ticket content pagination is invalid",
			false,
		)
		return agentTicketContentListQuery{}, false
	}
	query.After, err = h.decodeTicketContentListCursor(
		query.Cursor,
		kind,
		scope,
		ticketID,
		query.Limit,
	)
	switch {
	case errors.Is(err, errAgentTicketContentCursorKey):
		WriteProblem(
			c,
			http.StatusServiceUnavailable,
			ProblemServiceUnavailable,
			"Ticket content pagination is unavailable",
			true,
		)
		return agentTicketContentListQuery{}, false
	case err != nil:
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"Ticket content cursor is invalid",
			false,
		)
		return agentTicketContentListQuery{}, false
	default:
		return query, true
	}
}

func parseAgentTicketContentListQuery(
	rawQuery string,
) (agentTicketContentListQuery, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return agentTicketContentListQuery{}, errAgentTicketContentListQuery
	}
	result := agentTicketContentListQuery{
		Limit: agentTicketContentListDefault,
	}
	for key, candidates := range values {
		if (key != "limit" && key != "cursor") ||
			len(candidates) != 1 ||
			candidates[0] == "" ||
			strings.TrimSpace(candidates[0]) != candidates[0] {
			return agentTicketContentListQuery{}, errAgentTicketContentListQuery
		}
	}
	if candidates, exists := values["limit"]; exists {
		limit, parseErr := strconv.Atoi(candidates[0])
		if parseErr != nil ||
			limit < 1 ||
			limit > agentTicketContentListMax {
			return agentTicketContentListQuery{}, errAgentTicketContentListQuery
		}
		result.Limit = limit
	}
	if candidates, exists := values["cursor"]; exists {
		cursor := candidates[0]
		if len(cursor) > 2048 ||
			strings.IndexFunc(cursor, unicode.IsSpace) >= 0 {
			return agentTicketContentListQuery{}, errAgentTicketContentListQuery
		}
		result.Cursor = cursor
	}
	return result, nil
}

func (h *APIHandler) decodeTicketContentListCursor(
	raw string,
	kind string,
	scope models.ProjectScope,
	ticketID uint,
	limit int,
) (*agentTicketContentListPosition, error) {
	codec := h.ticketContentListCursorCodec(kind)
	if codec == nil {
		return nil, errAgentTicketContentCursorKey
	}
	if raw == "" {
		return nil, nil
	}
	var cursor agentTicketContentCursor
	if err := codec.Decode(raw, &cursor); err != nil {
		return nil, errAgentTicketContentListCursor
	}
	if cursor.Version != agentTicketContentCursorV1 ||
		cursor.Kind != kind ||
		cursor.Organization != scope.OrganizationID ||
		cursor.Project != scope.ProjectID ||
		cursor.Ticket != ticketID ||
		cursor.Limit != limit ||
		cursor.FilterHash != agentTicketContentFilterHash(ticketID) ||
		cursor.SortVersion != agentTicketContentSortV1 ||
		cursor.ID == 0 ||
		cursor.CreatedAt == "" ||
		strings.TrimSpace(cursor.CreatedAt) != cursor.CreatedAt {
		return nil, errAgentTicketContentListCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	if err != nil || createdAt.IsZero() {
		return nil, errAgentTicketContentListCursor
	}
	return &agentTicketContentListPosition{
		CreatedAt: createdAt,
		ID:        cursor.ID,
	}, nil
}

func (h *APIHandler) encodeTicketContentListCursor(
	kind string,
	scope models.ProjectScope,
	ticketID uint,
	limit int,
	position agentTicketContentListPosition,
) (string, error) {
	codec := h.ticketContentListCursorCodec(kind)
	if codec == nil {
		return "", errAgentTicketContentCursorKey
	}
	if position.ID == 0 || position.CreatedAt.IsZero() {
		return "", errAgentTicketContentListCursor
	}
	return codec.Encode(agentTicketContentCursor{
		Version:      agentTicketContentCursorV1,
		Kind:         kind,
		Organization: scope.OrganizationID,
		Project:      scope.ProjectID,
		Ticket:       ticketID,
		Limit:        limit,
		FilterHash:   agentTicketContentFilterHash(ticketID),
		SortVersion:  agentTicketContentSortV1,
		CreatedAt:    position.CreatedAt.UTC().Format(time.RFC3339Nano),
		ID:           position.ID,
	})
}

func (h *APIHandler) ticketContentListCursorCodec(
	kind string,
) *listcursor.Codec {
	if h == nil || h.ticketContentLists == nil {
		return nil
	}
	switch kind {
	case agentTicketCommentsList:
		return h.ticketContentLists.comments
	case agentTicketAttachmentsList:
		return h.ticketContentLists.attachments
	default:
		return nil
	}
}

func agentTicketContentFilterHash(ticketID uint) string {
	digest := sha256.Sum256([]byte(
		"ticket_id=" + strconv.FormatUint(uint64(ticketID), 10),
	))
	return hex.EncodeToString(digest[:])
}

type ticketContentListRecord interface {
	models.TicketComment | models.TicketAttachment
}

func nextTicketContentListCursor[
	T ticketContentListRecord,
](
	h *APIHandler,
	c *gin.Context,
	kind string,
	scope models.ProjectScope,
	ticketID uint,
	limit int,
	hasMore bool,
	items []T,
) (string, bool) {
	if !hasMore || len(items) == 0 {
		return "", true
	}
	lastCreatedAt, lastID := ticketContentListIdentity(items[len(items)-1])
	nextCursor, err := h.encodeTicketContentListCursor(
		kind,
		scope,
		ticketID,
		limit,
		agentTicketContentListPosition{
			CreatedAt: lastCreatedAt,
			ID:        lastID,
		},
	)
	if err != nil {
		WriteProblem(
			c,
			http.StatusInternalServerError,
			ProblemInternal,
			"Ticket content pagination failed",
			true,
		)
		return "", false
	}
	return nextCursor, true
}

func ticketContentListIdentity[T ticketContentListRecord](
	item T,
) (time.Time, uint) {
	switch value := any(item).(type) {
	case models.TicketComment:
		return value.CreatedAt, value.ID
	case models.TicketAttachment:
		return value.CreatedAt, value.ID
	default:
		return time.Time{}, 0
	}
}

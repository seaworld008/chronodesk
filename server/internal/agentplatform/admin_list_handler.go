package agentplatform

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *AdminHandler) OverviewMetrics(c *gin.Context) {
	scope, ok := h.requireProjectScope(c)
	if !ok {
		return
	}
	if c.Request.URL.RawQuery != "" {
		h.writeAdminListError(c, ErrInvalidAdminListQuery)
		return
	}
	if h.lists == nil {
		h.writeAdminListError(c, ErrAdminListCursorKey)
		return
	}
	metrics, err := h.lists.Overview(
		c.Request.Context(),
		scope,
		h.control,
	)
	if err != nil {
		h.writeAdminListError(c, err)
		return
	}
	WriteData(c, http.StatusOK, metrics, Meta{})
}

func (h *AdminHandler) ListPrincipalsPage(c *gin.Context) {
	scope, ok := h.requireProjectScope(c)
	if !ok {
		return
	}
	query, ok := h.requireAdminPageQuery(c, "created_at", "desc")
	if !ok {
		return
	}
	page, err := h.lists.ListPrincipals(
		c.Request.Context(),
		scope,
		query,
	)
	if err != nil {
		h.writeAdminListError(c, err)
		return
	}
	WriteData(c, http.StatusOK, page, Meta{})
}

func (h *AdminHandler) ListPoliciesPage(c *gin.Context) {
	scope, ok := h.requireProjectScope(c)
	if !ok {
		return
	}
	query, ok := h.requireAdminPageQuery(c, "priority", "desc")
	if !ok {
		return
	}
	page, err := h.lists.ListPolicies(
		c.Request.Context(),
		scope,
		c.Param("id"),
		query,
	)
	if err != nil {
		h.writeAdminListError(c, err)
		return
	}
	WriteData(c, http.StatusOK, page, Meta{})
}

func (h *AdminHandler) ListLeasesPage(c *gin.Context) {
	scope, ok := h.requireProjectScope(c)
	if !ok {
		return
	}
	query, ok := h.requireAdminPageQuery(c, "expires_at", "asc")
	if !ok {
		return
	}
	page, err := h.lists.ListLeases(
		c.Request.Context(),
		scope,
		query,
	)
	if err != nil {
		h.writeAdminListError(c, err)
		return
	}
	WriteData(c, http.StatusOK, page, Meta{})
}

func (h *AdminHandler) ListOutboxPage(c *gin.Context) {
	scope, ok := h.requireProjectScope(c)
	if !ok {
		return
	}
	query, ok := h.requireAdminPageQuery(c, "created_at", "desc")
	if !ok {
		return
	}
	page, err := h.lists.ListOutbox(
		c.Request.Context(),
		scope,
		query,
	)
	if err != nil {
		h.writeAdminListError(c, err)
		return
	}
	WriteData(c, http.StatusOK, page, Meta{})
}

func (h *AdminHandler) ListAttachmentScansPage(c *gin.Context) {
	scope, ok := h.requireProjectScope(c)
	if !ok {
		return
	}
	query, ok := h.requireAdminPageQuery(c, "created_at", "desc")
	if !ok {
		return
	}
	page, err := h.lists.ListAttachments(
		c.Request.Context(),
		scope,
		query,
	)
	if err != nil {
		h.writeAdminListError(c, err)
		return
	}
	WriteData(c, http.StatusOK, page, Meta{})
}

func (h *AdminHandler) ListDomainEventsPage(c *gin.Context) {
	scope, ok := h.requireProjectScope(c)
	if !ok {
		return
	}
	query, ok := h.requireAdminCursorQuery(c)
	if !ok {
		return
	}
	page, err := h.lists.ListDomainEvents(
		c.Request.Context(),
		scope,
		query,
	)
	if err != nil {
		h.writeAdminListError(c, err)
		return
	}
	WriteData(c, http.StatusOK, page, Meta{})
}

func (h *AdminHandler) ListPolicyDecisionsPage(c *gin.Context) {
	scope, ok := h.requireProjectScope(c)
	if !ok {
		return
	}
	query, ok := h.requireAdminCursorQuery(c)
	if !ok {
		return
	}
	page, err := h.lists.ListPolicyDecisions(
		c.Request.Context(),
		scope,
		query,
	)
	if err != nil {
		h.writeAdminListError(c, err)
		return
	}
	WriteData(c, http.StatusOK, page, Meta{})
}

func (h *AdminHandler) requireAdminPageQuery(
	c *gin.Context,
	sortBy string,
	sortOrder string,
) (AdminPageQuery, bool) {
	if h == nil || h.lists == nil {
		h.writeAdminListError(c, ErrAdminListCursorKey)
		return AdminPageQuery{}, false
	}
	parsed, err := parseAdminListQuery(
		c.Request.URL.RawQuery,
		adminPageListQuery,
	)
	if err != nil {
		h.writeAdminListError(c, err)
		return AdminPageQuery{}, false
	}
	if parsed.SortBy == "" {
		parsed.SortBy = sortBy
	}
	if parsed.SortOrder == "" {
		parsed.SortOrder = sortOrder
	}
	if parsed.SortBy != sortBy || parsed.SortOrder != sortOrder {
		h.writeAdminListError(c, ErrInvalidAdminListQuery)
		return AdminPageQuery{}, false
	}
	return parsed.AdminPageQuery, true
}

func (h *AdminHandler) requireAdminCursorQuery(
	c *gin.Context,
) (AdminCursorQuery, bool) {
	if h == nil || h.lists == nil {
		h.writeAdminListError(c, ErrAdminListCursorKey)
		return AdminCursorQuery{}, false
	}
	parsed, err := parseAdminListQuery(
		c.Request.URL.RawQuery,
		adminCursorListQuery,
	)
	if err != nil {
		h.writeAdminListError(c, err)
		return AdminCursorQuery{}, false
	}
	return parsed.AdminCursorQuery, true
}

func (h *AdminHandler) writeAdminListError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidAdminListQuery),
		errors.Is(err, ErrInvalidAdminListCursor):
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"Administrator list query is invalid",
			false,
		)
	case errors.Is(err, ErrAdminListCursorKey):
		WriteProblem(
			c,
			http.StatusServiceUnavailable,
			ProblemServiceUnavailable,
			"Administrator list service is unavailable",
			true,
		)
	default:
		h.writeNativeError(c, err)
	}
}

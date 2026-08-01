package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

const agentCollaborationRequestBodyLimit = 64 << 10

type agentCollaborationQueries interface {
	ListAgentRuns(
		context.Context,
		services.ProjectAccess,
		services.CollaborationPagination,
	) (*services.CollaborationPage[services.AgentRunSummary], error)
	GetAgentRun(
		context.Context,
		services.ProjectAccess,
		string,
	) (*services.AgentRunDetail, error)
	ListActionProposals(
		context.Context,
		services.ProjectAccess,
		services.CollaborationPagination,
	) (*services.CollaborationPage[services.ActionProposalSummary], error)
	GetActionProposal(
		context.Context,
		services.ProjectAccess,
		string,
	) (*services.ActionProposalDetail, error)
	ListApprovalTasks(
		context.Context,
		services.ProjectAccess,
		services.CollaborationPagination,
	) (*services.CollaborationPage[services.ApprovalTaskSummary], error)
	GetApprovalTask(
		context.Context,
		services.ProjectAccess,
		string,
	) (*services.ApprovalTaskDetail, error)
	ListHandoffs(
		context.Context,
		services.ProjectAccess,
		services.CollaborationPagination,
	) (*services.CollaborationPage[services.HandoffSummary], error)
	GetHandoff(
		context.Context,
		services.ProjectAccess,
		string,
	) (*services.HandoffDetail, error)
}

type agentCollaborationCommands interface {
	DecideApproval(
		context.Context,
		services.DecideApprovalInput,
	) (*models.ApprovalTask, error)
	TakeoverAgentRun(
		context.Context,
		services.TakeoverAgentRunInput,
	) (*models.Handoff, error)
}

// AgentCollaborationHandler is the project-scoped human adapter for the AI
// collaboration workbench. ProjectScopeMiddleware must run before these
// routes; request bodies can neither choose a project nor choose an Actor.
type AgentCollaborationHandler struct {
	queries  agentCollaborationQueries
	commands agentCollaborationCommands
	response *middleware.ResponseHelper
}

func NewAgentCollaborationHandler(
	queries *services.AgentCollaborationQueryService,
	commands *services.AgentCollaborationService,
) *AgentCollaborationHandler {
	return &AgentCollaborationHandler{
		queries:  queries,
		commands: commands,
		response: middleware.NewResponseHelper(),
	}
}

// RegisterRoutes mounts the workbench below a project route group that already
// uses ProjectScopeMiddleware.
func (handler *AgentCollaborationHandler) RegisterRoutes(
	projectGroup *gin.RouterGroup,
) {
	collaboration := projectGroup.Group("/agent-collaboration")
	collaboration.GET("/runs", handler.ListAgentRuns)
	collaboration.GET("/runs/:runID", handler.GetAgentRun)
	collaboration.GET("/proposals", handler.ListActionProposals)
	collaboration.GET("/proposals/:proposalID", handler.GetActionProposal)
	collaboration.GET("/approvals", handler.ListApprovalTasks)
	collaboration.GET("/approvals/:approvalID", handler.GetApprovalTask)
	collaboration.POST(
		"/approvals/:approvalID/decisions",
		handler.DecideApproval,
	)
	collaboration.GET("/handoffs", handler.ListHandoffs)
	collaboration.GET("/handoffs/:handoffID", handler.GetHandoff)
	collaboration.POST("/runs/:runID/takeover", handler.TakeoverAgentRun)
}

func (handler *AgentCollaborationHandler) ListAgentRuns(c *gin.Context) {
	access, ok := handler.requireReadAccess(c)
	if !ok {
		return
	}
	pagination, ok := handler.pagination(c)
	if !ok {
		return
	}
	page, err := handler.queries.ListAgentRuns(
		c.Request.Context(),
		access,
		pagination,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.List(
		c,
		page.Items,
		page.Total,
		page.Page,
		page.PageSize,
		"获取 Agent 运行记录成功",
	)
}

func (handler *AgentCollaborationHandler) GetAgentRun(c *gin.Context) {
	access, ok := handler.requireReadAccess(c)
	if !ok {
		return
	}
	runID, ok := handler.collaborationID(c, "runID")
	if !ok {
		return
	}
	run, err := handler.queries.GetAgentRun(
		c.Request.Context(),
		access,
		runID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, run, "获取 Agent 运行详情成功")
}

func (handler *AgentCollaborationHandler) ListActionProposals(
	c *gin.Context,
) {
	access, ok := handler.requireReadAccess(c)
	if !ok {
		return
	}
	pagination, ok := handler.pagination(c)
	if !ok {
		return
	}
	page, err := handler.queries.ListActionProposals(
		c.Request.Context(),
		access,
		pagination,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.List(
		c,
		page.Items,
		page.Total,
		page.Page,
		page.PageSize,
		"获取行动提案成功",
	)
}

func (handler *AgentCollaborationHandler) GetActionProposal(
	c *gin.Context,
) {
	access, ok := handler.requireReadAccess(c)
	if !ok {
		return
	}
	proposalID, ok := handler.collaborationID(c, "proposalID")
	if !ok {
		return
	}
	proposal, err := handler.queries.GetActionProposal(
		c.Request.Context(),
		access,
		proposalID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, proposal, "获取行动提案详情成功")
}

func (handler *AgentCollaborationHandler) ListApprovalTasks(
	c *gin.Context,
) {
	access, ok := handler.requireReadAccess(c)
	if !ok {
		return
	}
	pagination, ok := handler.pagination(c)
	if !ok {
		return
	}
	page, err := handler.queries.ListApprovalTasks(
		c.Request.Context(),
		access,
		pagination,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.List(
		c,
		page.Items,
		page.Total,
		page.Page,
		page.PageSize,
		"获取审批任务成功",
	)
}

func (handler *AgentCollaborationHandler) GetApprovalTask(
	c *gin.Context,
) {
	access, ok := handler.requireReadAccess(c)
	if !ok {
		return
	}
	approvalID, ok := handler.collaborationID(c, "approvalID")
	if !ok {
		return
	}
	approval, err := handler.queries.GetApprovalTask(
		c.Request.Context(),
		access,
		approvalID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, approval, "获取审批任务详情成功")
}

type approvalDecisionRequest struct {
	Decision models.ApprovalDecisionValue `json:"decision"`
	Comment  string                       `json:"comment"`
}

func (handler *AgentCollaborationHandler) DecideApproval(c *gin.Context) {
	access, ok := handler.requireWriteAccess(
		c,
		map[models.ProjectRole]struct{}{
			models.ProjectRoleAdmin:   {},
			models.ProjectRoleManager: {},
		},
		"仅项目管理员或经理可审批 Agent 行动",
	)
	if !ok {
		return
	}
	approvalID, ok := handler.collaborationID(c, "approvalID")
	if !ok {
		return
	}
	var request approvalDecisionRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	if request.Decision != models.ApprovalDecisionApprove &&
		request.Decision != models.ApprovalDecisionReject {
		handler.writeProblem(
			c,
			http.StatusBadRequest,
			"invalid_approval_decision",
			"审批决定只能是 approve 或 reject",
		)
		return
	}
	request.Comment = strings.TrimSpace(request.Comment)
	if utf8.RuneCountInString(request.Comment) > 1000 {
		handler.writeProblem(
			c,
			http.StatusBadRequest,
			"invalid_approval_comment",
			"审批备注不能超过 1000 个字符",
		)
		return
	}
	if _, err := handler.commands.DecideApproval(
		c.Request.Context(),
		services.DecideApprovalInput{
			ApprovalTaskID: approvalID,
			Decision:       request.Decision,
			Comment:        request.Comment,
		},
	); err != nil {
		handler.writeError(c, err)
		return
	}
	approval, err := handler.queries.GetApprovalTask(
		c.Request.Context(),
		access,
		approvalID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, approval, "审批决定已记录")
}

type agentRunTakeoverRequest struct {
	Reason             string   `json:"reason"`
	CompletedSummary   string   `json:"completed_summary"`
	MissingInformation []string `json:"missing_information"`
	EvidenceDigest     string   `json:"evidence_digest"`
}

func (handler *AgentCollaborationHandler) TakeoverAgentRun(
	c *gin.Context,
) {
	access, ok := handler.requireWriteAccess(
		c,
		map[models.ProjectRole]struct{}{
			models.ProjectRoleAdmin:   {},
			models.ProjectRoleManager: {},
			models.ProjectRoleAgent:   {},
		},
		"仅项目管理员、经理或 Agent 可人工接管",
	)
	if !ok {
		return
	}
	runID, ok := handler.collaborationID(c, "runID")
	if !ok {
		return
	}
	var request agentRunTakeoverRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	request.CompletedSummary = strings.TrimSpace(request.CompletedSummary)
	request.EvidenceDigest = strings.TrimSpace(request.EvidenceDigest)
	if request.Reason == "" ||
		utf8.RuneCountInString(request.Reason) > 1000 {
		handler.writeProblem(
			c,
			http.StatusBadRequest,
			"invalid_takeover_reason",
			"接管原因不能为空且不能超过 1000 个字符",
		)
		return
	}
	if utf8.RuneCountInString(request.CompletedSummary) > 10000 {
		handler.writeProblem(
			c,
			http.StatusBadRequest,
			"invalid_completed_summary",
			"已完成工作摘要不能超过 10000 个字符",
		)
		return
	}
	if !validEvidenceDigest(request.EvidenceDigest) {
		handler.writeProblem(
			c,
			http.StatusBadRequest,
			"invalid_evidence_digest",
			"证据摘要格式无效",
		)
		return
	}
	if len(request.MissingInformation) > 50 {
		handler.writeProblem(
			c,
			http.StatusBadRequest,
			"invalid_missing_information",
			"缺失信息不能超过 50 项",
		)
		return
	}
	for index, value := range request.MissingInformation {
		value = strings.TrimSpace(value)
		if value == "" || utf8.RuneCountInString(value) > 2000 {
			handler.writeProblem(
				c,
				http.StatusBadRequest,
				"invalid_missing_information",
				"缺失信息不能为空且每项不能超过 2000 个字符",
			)
			return
		}
		request.MissingInformation[index] = value
	}
	handoff, err := handler.commands.TakeoverAgentRun(
		c.Request.Context(),
		services.TakeoverAgentRunInput{
			AgentRunID:         runID,
			Reason:             request.Reason,
			CompletedSummary:   request.CompletedSummary,
			MissingInformation: request.MissingInformation,
			EvidenceDigest:     request.EvidenceDigest,
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	result, err := handler.queries.GetHandoff(
		c.Request.Context(),
		access,
		handoff.ID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, result, "Agent 运行已由人工接管")
}

func (handler *AgentCollaborationHandler) ListHandoffs(c *gin.Context) {
	access, ok := handler.requireReadAccess(c)
	if !ok {
		return
	}
	pagination, ok := handler.pagination(c)
	if !ok {
		return
	}
	page, err := handler.queries.ListHandoffs(
		c.Request.Context(),
		access,
		pagination,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.List(
		c,
		page.Items,
		page.Total,
		page.Page,
		page.PageSize,
		"获取交接记录成功",
	)
}

func (handler *AgentCollaborationHandler) GetHandoff(c *gin.Context) {
	access, ok := handler.requireReadAccess(c)
	if !ok {
		return
	}
	handoffID, ok := handler.collaborationID(c, "handoffID")
	if !ok {
		return
	}
	handoff, err := handler.queries.GetHandoff(
		c.Request.Context(),
		access,
		handoffID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, handoff, "获取交接详情成功")
}

func (handler *AgentCollaborationHandler) requireReadAccess(
	c *gin.Context,
) (services.ProjectAccess, bool) {
	if handler == nil || handler.queries == nil {
		middleware.NewResponseHelper().Error(
			c,
			http.StatusServiceUnavailable,
			"AI 协作服务不可用",
		)
		return services.ProjectAccess{}, false
	}
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.writeProblem(
			c,
			http.StatusForbidden,
			"collaboration_access_denied",
			"未解析可信项目范围",
		)
		return services.ProjectAccess{}, false
	}
	userID := c.GetUint("user_id")
	operation, err := services.OperationContextFromContext(c.Request.Context())
	if userID == 0 ||
		err != nil ||
		operation.Scope != access.Scope ||
		operation.Source != services.SourceProtocolHumanREST ||
		operation.Actor != models.HumanActor(userID) ||
		!access.Role.IsValid() {
		handler.writeProblem(
			c,
			http.StatusForbidden,
			"collaboration_access_denied",
			"AI 协作操作上下文无效",
		)
		return services.ProjectAccess{}, false
	}
	return access, true
}

func (handler *AgentCollaborationHandler) requireWriteAccess(
	c *gin.Context,
	allowed map[models.ProjectRole]struct{},
	message string,
) (services.ProjectAccess, bool) {
	access, ok := handler.requireReadAccess(c)
	if !ok {
		return services.ProjectAccess{}, false
	}
	if handler.commands == nil {
		handler.writeProblem(
			c,
			http.StatusServiceUnavailable,
			"collaboration_service_unavailable",
			"AI 协作命令服务不可用",
		)
		return services.ProjectAccess{}, false
	}
	if _, permitted := allowed[access.Role]; !permitted {
		handler.writeProblem(
			c,
			http.StatusForbidden,
			"collaboration_write_denied",
			message,
		)
		return services.ProjectAccess{}, false
	}
	return access, true
}

func (handler *AgentCollaborationHandler) pagination(
	c *gin.Context,
) (services.CollaborationPagination, bool) {
	page, pageSize, ok := parseStrictPagePagination(c, 25, 100)
	if !ok {
		return services.CollaborationPagination{}, false
	}
	return services.CollaborationPagination{
		Page:     page,
		PageSize: pageSize,
	}, true
}

func (handler *AgentCollaborationHandler) collaborationID(
	c *gin.Context,
	name string,
) (string, bool) {
	raw := strings.TrimSpace(c.Param(name))
	parsed, err := uuid.Parse(raw)
	if err != nil {
		handler.writeProblem(
			c,
			http.StatusBadRequest,
			"invalid_collaboration_id",
			"协作资源标识无效",
		)
		return "", false
	}
	return parsed.String(), true
}

func (handler *AgentCollaborationHandler) bindJSON(
	c *gin.Context,
	destination any,
) bool {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		agentCollaborationRequestBodyLimit,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		handler.writeProblem(
			c,
			http.StatusBadRequest,
			"invalid_collaboration_request",
			"AI 协作请求正文必须是有效的 JSON 对象",
		)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		handler.writeProblem(
			c,
			http.StatusBadRequest,
			"invalid_collaboration_request",
			"AI 协作请求只能包含一个 JSON 对象",
		)
		return false
	}
	return true
}

func (handler *AgentCollaborationHandler) writeError(
	c *gin.Context,
	err error,
) {
	switch {
	case errors.Is(err, services.ErrCollaborationAccessDenied):
		handler.writeProblem(
			c,
			http.StatusForbidden,
			"collaboration_access_denied",
			"无权访问 AI 协作资源",
		)
	case errors.Is(err, services.ErrCollaborationNotFound),
		errors.Is(err, gorm.ErrRecordNotFound):
		handler.writeProblem(
			c,
			http.StatusNotFound,
			"collaboration_not_found",
			"AI 协作资源不存在",
		)
	case errors.Is(err, services.ErrApprovalInvalidated):
		handler.writeProblem(
			c,
			http.StatusConflict,
			"approval_invalidated",
			"审批绑定的提案或工单版本已失效",
		)
	case errors.Is(err, services.ErrApprovalExpired):
		handler.writeProblem(
			c,
			http.StatusConflict,
			"approval_expired",
			"审批任务已过期",
		)
	case errors.Is(err, services.ErrProposalApprovalRequired),
		errors.Is(err, services.ErrProposalNotExecutable),
		isCollaborationStateConflict(err):
		handler.writeProblem(
			c,
			http.StatusConflict,
			"collaboration_state_conflict",
			"AI 协作资源状态已变化，请刷新后重试",
		)
	default:
		logHandlerFailure(c, "agent_collaboration.operation", err)
		handler.writeProblem(
			c,
			http.StatusInternalServerError,
			"collaboration_internal_error",
			"AI 协作操作失败，请稍后重试",
		)
	}
}

func isCollaborationStateConflict(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "changed concurrently") ||
		strings.Contains(err.Error(), "terminal agent run") ||
		strings.Contains(err.Error(), "UNIQUE constraint") ||
		strings.Contains(err.Error(), "duplicate key")
}

func (handler *AgentCollaborationHandler) writeProblem(
	c *gin.Context,
	status int,
	code string,
	message string,
) {
	c.JSON(status, gin.H{
		"code": code,
		"msg":  message,
	})
}

func validEvidenceDigest(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune("-_.:", character):
		default:
			return false
		}
	}
	return true
}

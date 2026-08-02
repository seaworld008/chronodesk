package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/database"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

const (
	projectAccessContextKey      = "project_access"
	projectRoleContextKey        = "project_role"
	projectAfterCommitContextKey = "project_after_commit"
)

var errProjectRequestRollback = errors.New(
	"project request returned an unsuccessful response",
)

type projectAfterCommitQueue struct {
	callbacks []func()
}

func queueProjectAfterCommit(c *gin.Context, callback func()) error {
	if c == nil || callback == nil {
		return errors.New("project after-commit callback is invalid")
	}
	value, exists := c.Get(projectAfterCommitContextKey)
	queue, ok := value.(*projectAfterCommitQueue)
	if !exists || !ok || queue == nil {
		return errors.New("project after-commit queue is unavailable")
	}
	queue.callbacks = append(queue.callbacks, callback)
	return nil
}

func runProjectAfterCommit(c *gin.Context) {
	if c == nil {
		return
	}
	value, exists := c.Get(projectAfterCommitContextKey)
	queue, ok := value.(*projectAfterCommitQueue)
	if !exists || !ok || queue == nil {
		return
	}
	callbacks := append([]func(){}, queue.callbacks...)
	queue.callbacks = nil
	for _, callback := range callbacks {
		func() {
			defer func() {
				if recover() != nil {
					_ = c.Error(errors.New(
						"project after-commit callback panicked",
					))
				}
			}()
			callback()
		}()
	}
}

type ProjectHandler struct {
	service  *services.ProjectService
	response *middleware.ResponseHelper
}

type projectQueueResponse struct {
	PublicID     string             `json:"public_id"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
	TeamPublicID *string            `json:"team_public_id,omitempty"`
	TeamName     *string            `json:"team_name,omitempty"`
	Key          models.QueueKey    `json:"key"`
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	Status       models.QueueStatus `json:"status"`
	IsDefault    bool               `json:"is_default"`
}

func NewProjectHandler(service *services.ProjectService) *ProjectHandler {
	return &ProjectHandler{
		service:  service,
		response: middleware.NewResponseHelper(),
	}
}

func (handler *ProjectHandler) List(c *gin.Context) {
	if handler.service == nil {
		handler.response.InternalServerError(c, "项目服务不可用")
		return
	}
	userID := c.GetUint("user_id")
	query, err := parseDirectoryListQuery(
		c.Request.URL.RawQuery,
		directoryListQuerySpec{
			DefaultSortBy:    "name",
			DefaultSortOrder: "asc",
			SortFields: map[string]struct{}{
				"name":       {},
				"key":        {},
				"created_at": {},
			},
			FilterFields: map[string]struct{}{"search": {}},
		},
	)
	if err != nil {
		handler.response.BadRequest(c, "授权项目查询参数无效")
		return
	}
	search, _ := query.value("search")
	projects, err := handler.service.ListHumanProjectPage(
		c.Request.Context(),
		userID,
		services.HumanProjectListRequest{
			Page:      query.Page,
			PageSize:  query.PageSize,
			Search:    search,
			SortBy:    query.SortBy,
			SortOrder: query.SortOrder,
		},
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, projects, "获取授权项目成功")
}

func (handler *ProjectHandler) Current(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return
	}
	handler.response.Success(c, access, "获取项目上下文成功")
}

func (handler *ProjectHandler) ListQueues(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return
	}
	query, err := parseDirectoryListQuery(
		c.Request.URL.RawQuery,
		directoryListQuerySpec{
			DefaultSortBy:    "is_default",
			DefaultSortOrder: "desc",
			SortFields: map[string]struct{}{
				"created_at": {},
				"updated_at": {},
				"name":       {},
				"key":        {},
				"is_default": {},
			},
		},
	)
	if err != nil {
		handler.response.BadRequest(c, "项目队列查询参数无效")
		return
	}
	queues, err := handler.service.ListQueuePage(
		c.Request.Context(),
		access.Scope,
		services.DirectoryPageRequest{
			Page:      query.Page,
			PageSize:  query.PageSize,
			SortBy:    query.SortBy,
			SortOrder: query.SortOrder,
		},
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	items := make([]projectQueueResponse, 0, len(queues.Items))
	for _, queue := range queues.Items {
		var teamPublicID *string
		var teamName *string
		if queue.Team != nil && queue.Team.PublicID != "" {
			publicID := queue.Team.PublicID
			name := queue.Team.Name
			teamPublicID = &publicID
			teamName = &name
		}
		items = append(items, projectQueueResponse{
			PublicID:     queue.PublicID,
			CreatedAt:    queue.CreatedAt,
			UpdatedAt:    queue.UpdatedAt,
			TeamPublicID: teamPublicID,
			TeamName:     teamName,
			Key:          queue.Key,
			Name:         queue.Name,
			Description:  queue.Description,
			Status:       queue.Status,
			IsDefault:    queue.IsDefault,
		})
	}
	handler.response.Success(
		c,
		services.DirectoryPage[projectQueueResponse]{
			Items:      items,
			Total:      queues.Total,
			Page:       queues.Page,
			PageSize:   queues.PageSize,
			TotalPages: queues.TotalPages,
		},
		"获取项目队列成功",
	)
}

func (handler *ProjectHandler) ListMemberships(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return
	}
	if access.Role != models.ProjectRoleAdmin &&
		access.Role != models.ProjectRoleManager {
		handler.response.Forbidden(c, "仅项目管理员或经理可查看项目成员")
		return
	}
	query, err := parseDirectoryListQuery(
		c.Request.URL.RawQuery,
		directoryListQuerySpec{
			DefaultSortBy:    "is_active",
			DefaultSortOrder: "desc",
			SortFields: map[string]struct{}{
				"created_at": {},
				"updated_at": {},
				"role":       {},
				"is_active":  {},
				"user_id":    {},
			},
		},
	)
	if err != nil {
		handler.response.BadRequest(c, "项目成员查询参数无效")
		return
	}
	memberships, err := handler.service.ListHumanMembershipPage(
		c.Request.Context(),
		access.Scope,
		services.DirectoryPageRequest{
			Page:      query.Page,
			PageSize:  query.PageSize,
			SortBy:    query.SortBy,
			SortOrder: query.SortOrder,
		},
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, memberships, "获取项目成员成功")
}

func (handler *ProjectHandler) SearchMembershipCandidates(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return
	}
	if access.Role != models.ProjectRoleAdmin {
		handler.response.Forbidden(c, "仅项目管理员可搜索成员候选人")
		return
	}
	request, err := parseProjectUserSearchQuery(c.Request.URL.Query())
	if err != nil {
		handler.response.BadRequest(c, "项目成员候选查询参数无效")
		return
	}
	users, err := handler.service.SearchHumanMembershipCandidates(
		c.Request.Context(),
		access.Scope,
		request,
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, users, "获取项目成员候选人成功")
}

type upsertProjectMembershipRequest struct {
	UserID               uint               `json:"user_id" binding:"required"`
	Role                 models.ProjectRole `json:"role" binding:"required"`
	KnowledgeContributor *bool              `json:"knowledge_contributor"`
	ExpectedVersion      *uint64            `json:"expected_version"`
}

func (handler *ProjectHandler) UpsertMembership(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return
	}
	if access.Role != models.ProjectRoleAdmin {
		handler.response.Forbidden(c, "仅项目管理员可变更项目成员")
		return
	}
	var request upsertProjectMembershipRequest
	if err := decodeStrictProjectJSON(c, &request); err != nil ||
		!request.Role.IsValid() {
		handler.response.BadRequest(c, "项目成员参数无效")
		return
	}
	if request.ExpectedVersion == nil {
		handler.response.Error(
			c,
			http.StatusPreconditionRequired,
			"缺少成员版本，请刷新成员列表后重试",
		)
		return
	}
	membership, err := handler.service.UpsertHumanMembership(
		c.Request.Context(),
		access.Scope,
		services.UpsertProjectMembershipInput{
			UserID:               request.UserID,
			Role:                 request.Role,
			KnowledgeContributor: request.KnowledgeContributor,
			ExpectedVersion:      *request.ExpectedVersion,
		},
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, membership, "项目成员授权成功")
}

func (handler *ProjectHandler) DeactivateMembership(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return
	}
	if access.Role != models.ProjectRoleAdmin {
		handler.response.Forbidden(c, "仅项目管理员可撤销项目成员")
		return
	}
	userID, err := strconv.ParseUint(c.Param("userID"), 10, 32)
	if err != nil || userID == 0 {
		handler.response.BadRequest(c, "用户 ID 无效")
		return
	}
	query := c.Request.URL.Query()
	if err := requireExactProjectQueryKeys(
		query,
		"expected_version",
	); err != nil {
		handler.response.BadRequest(c, "成员版本参数无效")
		return
	}
	expectedVersionValues, exists := query["expected_version"]
	if !exists ||
		len(expectedVersionValues) != 1 ||
		strings.TrimSpace(expectedVersionValues[0]) == "" {
		handler.response.Error(
			c,
			http.StatusPreconditionRequired,
			"缺少成员版本，请刷新成员列表后重试",
		)
		return
	}
	expectedVersion, err := strconv.ParseUint(
		expectedVersionValues[0],
		10,
		64,
	)
	if err != nil {
		handler.response.BadRequest(c, "成员版本参数无效")
		return
	}
	membership, err := handler.service.DeactivateHumanMembership(
		c.Request.Context(),
		access.Scope,
		uint(userID),
		expectedVersion,
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, membership, "项目成员授权已撤销")
}

type createProjectRequest struct {
	BusinessUnitPublicID    string `json:"business_unit_public_id" binding:"required"`
	Key                     string `json:"key" binding:"required"`
	Name                    string `json:"name" binding:"required,max=120"`
	Description             string `json:"description" binding:"max=500"`
	InitialAdministratorIDs []uint `json:"initial_project_admin_user_ids" binding:"required,min=1,max=100,dive,gt=0"`
	DefaultQueueKey         string `json:"default_queue_key" binding:"required"`
	DefaultQueueName        string `json:"default_queue_name" binding:"required,max=120"`
}

// platformProjectSummary keeps the existing command-handler helper private
// while sharing the exact closed DTO used by the platform list service.
type platformProjectSummary = services.PlatformProjectSummary

func newPlatformProjectSummary(
	project models.Project,
) platformProjectSummary {
	return platformProjectSummary{
		PublicID:    project.PublicID,
		CreatedAt:   project.CreatedAt,
		UpdatedAt:   project.UpdatedAt,
		Key:         project.Key,
		Name:        project.Name,
		Description: project.Description,
		Status:      project.Status,
		BusinessUnit: services.PlatformBusinessUnitSummary{
			PublicID:    project.BusinessUnit.PublicID,
			Key:         project.BusinessUnit.Key,
			Name:        project.BusinessUnit.Name,
			Description: project.BusinessUnit.Description,
		},
	}
}

func (handler *ProjectHandler) ListPlatform(c *gin.Context) {
	request, err := parsePlatformProjectListQuery(c.Request.URL.Query())
	if err != nil {
		handler.response.BadRequest(c, "平台项目查询参数无效")
		return
	}
	if handler.service == nil {
		handler.response.InternalServerError(c, "项目服务不可用")
		return
	}
	projects, err := handler.service.ListPlatformProjectPage(
		c.Request.Context(),
		c.GetUint("user_id"),
		request,
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, projects, "获取平台项目成功")
}

func (handler *ProjectHandler) CreationContext(c *gin.Context) {
	request, err := parseProjectCreationContextQuery(c.Request.URL.Query())
	if err != nil {
		handler.response.BadRequest(c, "项目创建上下文查询参数无效")
		return
	}
	if handler.service == nil {
		handler.response.InternalServerError(c, "项目服务不可用")
		return
	}
	options, err := handler.service.GetProjectCreationContext(
		c.Request.Context(),
		c.GetUint("user_id"),
		request,
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, options, "获取项目创建上下文成功")
}

func (handler *ProjectHandler) ListPlatformBusinessUnits(c *gin.Context) {
	request, err := parsePlatformBusinessUnitSearchQuery(
		c.Request.URL.Query(),
	)
	if err != nil {
		handler.response.BadRequest(c, "平台业务单元查询参数无效")
		return
	}
	if handler.service == nil {
		handler.response.InternalServerError(c, "项目服务不可用")
		return
	}
	units, err := handler.service.ListPlatformProjectBusinessUnits(
		c.Request.Context(),
		c.GetUint("user_id"),
		request,
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, units, "获取平台业务单元成功")
}

func (handler *ProjectHandler) Create(c *gin.Context) {
	var request createProjectRequest
	if err := decodeStrictProjectJSON(c, &request); err != nil ||
		models.ValidateProjectKey(request.Key) != nil ||
		!canonicalProjectPublicID(request.BusinessUnitPublicID) ||
		!uniquePositiveUserIDs(request.InitialAdministratorIDs) ||
		models.ValidateQueueKey(
			strings.TrimSpace(request.DefaultQueueKey),
		) != nil ||
		strings.TrimSpace(request.DefaultQueueName) == "" {
		handler.response.BadRequest(c, "项目参数无效")
		return
	}
	if handler.service == nil {
		handler.response.InternalServerError(c, "项目服务不可用")
		return
	}
	project, err := handler.service.CreateProject(
		c.Request.Context(),
		services.CreateProjectInput{
			ActorUserID:             c.GetUint("user_id"),
			BusinessUnitPublicID:    request.BusinessUnitPublicID,
			Key:                     request.Key,
			Name:                    request.Name,
			Description:             request.Description,
			InitialAdministratorIDs: request.InitialAdministratorIDs,
			DefaultQueueKey: strings.TrimSpace(
				request.DefaultQueueKey,
			),
			DefaultQueueName: request.DefaultQueueName,
		},
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Created(
		c,
		newPlatformProjectSummary(*project),
		"项目创建成功",
	)
}

func (handler *ProjectHandler) Archive(c *gin.Context) {
	projectPublicID := c.Param("projectPublicID")
	parsedPublicID, err := uuid.Parse(projectPublicID)
	if err != nil ||
		parsedPublicID.Version() != 7 ||
		parsedPublicID.Variant() != uuid.RFC4122 ||
		parsedPublicID.String() != projectPublicID {
		handler.response.BadRequest(c, "项目公共 ID 无效")
		return
	}
	if handler.service == nil {
		handler.response.InternalServerError(c, "项目服务不可用")
		return
	}
	project, err := handler.service.ArchiveProject(
		c.Request.Context(),
		projectPublicID,
		models.HumanActor(c.GetUint("user_id")),
	)
	if err != nil {
		writeProjectArchiveError(c, handler.response, err)
		return
	}
	if project == nil {
		handler.response.InternalServerError(c, "项目归档失败")
		return
	}
	handler.response.Success(
		c,
		newPlatformProjectSummary(*project),
		"项目已归档",
	)
}

func decodeStrictProjectJSON(c *gin.Context, target interface{}) error {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return errors.New("JSON request body is required")
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON request body must contain exactly one value")
		}
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

func parsePlatformProjectListQuery(
	query url.Values,
) (services.PlatformProjectListRequest, error) {
	if err := requireExactProjectQueryKeys(
		query,
		"page",
		"page_size",
		"search",
		"status",
		"business_unit_public_id",
		"order_by",
		"order",
	); err != nil {
		return services.PlatformProjectListRequest{}, err
	}
	page, err := positiveProjectQueryInteger(query, "page", 1, 0)
	if err != nil {
		return services.PlatformProjectListRequest{}, err
	}
	pageSize, err := positiveProjectQueryInteger(
		query,
		"page_size",
		25,
		100,
	)
	if err != nil {
		return services.PlatformProjectListRequest{}, err
	}
	if page > math.MaxInt/pageSize {
		return services.PlatformProjectListRequest{}, errors.New(
			"project page offset overflows",
		)
	}
	search, err := singleProjectQueryValue(query, "search", "")
	if err != nil || utf8.RuneCountInString(strings.TrimSpace(search)) > 100 {
		return services.PlatformProjectListRequest{}, errors.New(
			"invalid project search",
		)
	}
	statusValue, err := singleProjectQueryValue(query, "status", "")
	if err != nil {
		return services.PlatformProjectListRequest{}, err
	}
	var status *models.ProjectStatus
	if statusValue != "" {
		parsed := models.ProjectStatus(statusValue)
		if parsed != models.ProjectStatusActive &&
			parsed != models.ProjectStatusArchived {
			return services.PlatformProjectListRequest{}, errors.New(
				"invalid project status",
			)
		}
		status = &parsed
	}
	businessUnitPublicID, err := singleProjectQueryValue(
		query,
		"business_unit_public_id",
		"",
	)
	if err != nil ||
		(businessUnitPublicID != "" &&
			!canonicalProjectPublicID(businessUnitPublicID)) {
		return services.PlatformProjectListRequest{}, errors.New(
			"invalid business unit public id",
		)
	}
	orderBy, err := singleProjectQueryValue(query, "order_by", "name")
	if err != nil {
		return services.PlatformProjectListRequest{}, err
	}
	switch orderBy {
	case "name", "key", "status", "business_unit", "created_at", "updated_at":
	default:
		return services.PlatformProjectListRequest{}, errors.New(
			"invalid project order field",
		)
	}
	order, err := singleProjectQueryValue(query, "order", "asc")
	if err != nil || (order != "asc" && order != "desc") {
		return services.PlatformProjectListRequest{}, errors.New(
			"invalid project order",
		)
	}
	return services.PlatformProjectListRequest{
		Page:                 page,
		PageSize:             pageSize,
		Search:               strings.TrimSpace(search),
		Status:               status,
		BusinessUnitPublicID: businessUnitPublicID,
		OrderBy:              orderBy,
		Order:                order,
	}, nil
}

func parseProjectUserSearchQuery(
	query url.Values,
) (services.ProjectUserSearchRequest, error) {
	if err := requireExactProjectQueryKeys(
		query,
		"page",
		"page_size",
		"search",
	); err != nil {
		return services.ProjectUserSearchRequest{}, err
	}
	page, err := positiveProjectQueryInteger(query, "page", 1, 0)
	if err != nil {
		return services.ProjectUserSearchRequest{}, err
	}
	pageSize, err := positiveProjectQueryInteger(
		query,
		"page_size",
		25,
		100,
	)
	if err != nil {
		return services.ProjectUserSearchRequest{}, err
	}
	if page > math.MaxInt/pageSize {
		return services.ProjectUserSearchRequest{}, errors.New(
			"project user page offset overflows",
		)
	}
	search, err := singleProjectQueryValue(query, "search", "")
	if err != nil || utf8.RuneCountInString(strings.TrimSpace(search)) > 100 {
		return services.ProjectUserSearchRequest{}, errors.New(
			"invalid user search",
		)
	}
	return services.ProjectUserSearchRequest{
		Page:     page,
		PageSize: pageSize,
		Search:   strings.TrimSpace(search),
	}, nil
}

func parsePlatformBusinessUnitSearchQuery(
	query url.Values,
) (services.PlatformBusinessUnitSearchRequest, error) {
	users, err := parseProjectUserSearchQuery(query)
	if err != nil {
		return services.PlatformBusinessUnitSearchRequest{}, err
	}
	return services.PlatformBusinessUnitSearchRequest{
		Page:     users.Page,
		PageSize: users.PageSize,
		Search:   users.Search,
	}, nil
}

func parseProjectCreationContextQuery(
	query url.Values,
) (services.ProjectCreationContextRequest, error) {
	if err := requireExactProjectQueryKeys(
		query,
		"page",
		"page_size",
		"search",
		"business_unit_page",
		"business_unit_page_size",
		"business_unit_search",
	); err != nil {
		return services.ProjectCreationContextRequest{}, err
	}
	usersQuery := make(url.Values)
	for _, key := range []string{"page", "page_size", "search"} {
		if values, ok := query[key]; ok {
			usersQuery[key] = values
		}
	}
	users, err := parseProjectUserSearchQuery(usersQuery)
	if err != nil {
		return services.ProjectCreationContextRequest{}, err
	}
	businessUnitPage, err := positiveProjectQueryInteger(
		query,
		"business_unit_page",
		1,
		0,
	)
	if err != nil {
		return services.ProjectCreationContextRequest{}, err
	}
	businessUnitPageSize, err := positiveProjectQueryInteger(
		query,
		"business_unit_page_size",
		25,
		100,
	)
	if err != nil {
		return services.ProjectCreationContextRequest{}, err
	}
	businessUnitSearch, err := singleProjectQueryValue(
		query,
		"business_unit_search",
		"",
	)
	if err != nil ||
		utf8.RuneCountInString(strings.TrimSpace(businessUnitSearch)) > 100 {
		return services.ProjectCreationContextRequest{}, errors.New(
			"invalid business unit search",
		)
	}
	return services.ProjectCreationContextRequest{
		Users: users,
		BusinessUnits: services.PlatformBusinessUnitSearchRequest{
			Page:     businessUnitPage,
			PageSize: businessUnitPageSize,
			Search:   strings.TrimSpace(businessUnitSearch),
		},
	}, nil
}

func requireExactProjectQueryKeys(
	query url.Values,
	allowed ...string,
) error {
	allowlist := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowlist[key] = struct{}{}
	}
	for key, values := range query {
		if _, ok := allowlist[key]; !ok || len(values) != 1 {
			return errors.New("invalid project query parameter")
		}
	}
	return nil
}

func positiveProjectQueryInteger(
	query url.Values,
	name string,
	defaultValue int,
	maximum int,
) (int, error) {
	raw, exists := query[name]
	if !exists {
		return defaultValue, nil
	}
	if len(raw) != 1 || raw[0] == "" {
		return 0, errors.New("invalid project pagination")
	}
	value, err := strconv.Atoi(raw[0])
	if err != nil || value < 1 || (maximum > 0 && value > maximum) {
		return 0, errors.New("invalid project pagination")
	}
	return value, nil
}

func singleProjectQueryValue(
	query url.Values,
	name string,
	defaultValue string,
) (string, error) {
	raw, exists := query[name]
	if !exists {
		return defaultValue, nil
	}
	if len(raw) != 1 {
		return "", errors.New("duplicate project query parameter")
	}
	return raw[0], nil
}

func canonicalProjectPublicID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil &&
		parsed.Version() == 7 &&
		parsed.Variant() == uuid.RFC4122 &&
		parsed.String() == value
}

func uniquePositiveUserIDs(userIDs []uint) bool {
	if len(userIDs) == 0 || len(userIDs) > 100 {
		return false
	}
	seen := make(map[uint]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID == 0 {
			return false
		}
		if _, duplicate := seen[userID]; duplicate {
			return false
		}
		seen[userID] = struct{}{}
	}
	return true
}

func ProjectScopeMiddleware(
	service *services.ProjectService,
	db *gorm.DB,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil || db == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"code": "project_service_unavailable",
				"msg":  "项目服务不可用",
			})
			return
		}
		userID := c.GetUint("user_id")
		access, err := service.ResolveHumanProject(
			c.Request.Context(),
			c.Param("projectKey"),
			userID,
		)
		if err != nil {
			response := middleware.NewResponseHelper()
			writeProjectScopeResolutionError(c, response, err)
			c.Abort()
			return
		}
		operation := services.OperationContext{
			Scope:         access.Scope,
			Actor:         models.HumanActor(userID),
			Source:        services.SourceProtocolHumanREST,
			TraceID:       middleware.TraceID(c),
			CorrelationID: middleware.CorrelationID(c),
		}
		requestContext, err := services.WithOperationContext(
			c.Request.Context(),
			operation,
		)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": "invalid_project_context",
				"msg":  "项目上下文无效",
			})
			return
		}
		c.Set(
			projectAfterCommitContextKey,
			&projectAfterCommitQueue{},
		)
		originalWriter := c.Writer
		defer func() {
			c.Writer = originalWriter
		}()
		bufferedWriter, err :=
			middleware.NewTransactionalResponseBuffer(originalWriter)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code": "project_response_unavailable",
				"msg":  "项目响应事务不可用",
			})
			return
		}
		defer func() {
			if closeErr := bufferedWriter.Close(); closeErr != nil {
				_ = c.Error(closeErr)
			}
		}()
		scopedErr := database.WithProjectScopeContextTransaction(
			requestContext,
			db,
			access.Scope,
			func(scopedContext context.Context) error {
				currentAccess, revalidateErr :=
					service.RevalidateHumanProjectAccess(
						scopedContext,
						access.Scope,
						userID,
					)
				if revalidateErr != nil {
					return revalidateErr
				}
				c.Set(projectAccessContextKey, *currentAccess)
				c.Set(
					projectRoleContextKey,
					string(currentAccess.Role),
				)
				c.Request = c.Request.WithContext(scopedContext)
				c.Writer = bufferedWriter
				c.Next()
				c.Writer = originalWriter
				if err := bufferedWriter.Err(); err != nil {
					return err
				}
				if c.IsAborted() ||
					bufferedWriter.Status() >= http.StatusBadRequest {
					return errProjectRequestRollback
				}
				return nil
			},
		)
		c.Writer = originalWriter
		// Never leave a completed transaction in the context observed by
		// middleware that resumes after c.Next.
		c.Request = c.Request.WithContext(requestContext)
		if errors.Is(scopedErr, errProjectRequestRollback) {
			if err := bufferedWriter.Commit(); err != nil {
				_ = c.Error(err)
				if !c.Writer.Written() {
					c.AbortWithStatusJSON(
						http.StatusInternalServerError,
						gin.H{
							"code": "project_response_failed",
							"msg":  "项目响应失败",
						},
					)
				}
			}
			return
		}
		if errors.Is(scopedErr, services.ErrProjectAccessDenied) ||
			errors.Is(scopedErr, services.ErrProjectInactive) {
			writeProjectScopeResolutionError(
				c,
				middleware.NewResponseHelper(),
				scopedErr,
			)
			return
		}
		if scopedErr != nil {
			_ = c.Error(scopedErr)
			if !c.Writer.Written() {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"code": "project_transaction_failed",
					"msg":  "项目操作事务失败",
				})
			} else {
				c.Abort()
			}
			return
		}
		runProjectAfterCommit(c)
		if err := bufferedWriter.Commit(); err != nil {
			_ = c.Error(err)
			if !c.Writer.Written() {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"code": "project_response_failed",
					"msg":  "项目响应失败",
				})
			} else {
				c.Abort()
			}
		}
	}
}

func ProjectAccessFromGin(c *gin.Context) (services.ProjectAccess, bool) {
	if c == nil {
		return services.ProjectAccess{}, false
	}
	access, ok := c.Get(projectAccessContextKey)
	if !ok {
		return services.ProjectAccess{}, false
	}
	resolved, ok := access.(services.ProjectAccess)
	return resolved, ok
}

// RequireProjectRoles authorizes an exact allowlist using the membership that
// ProjectScopeMiddleware resolved for this request. Project roles are not a
// hierarchy and platform duties never participate in this decision.
func RequireProjectRoles(allowedRoles ...models.ProjectRole) gin.HandlerFunc {
	allowlist := make(map[models.ProjectRole]struct{}, len(allowedRoles))
	for _, role := range allowedRoles {
		if role.IsValid() {
			allowlist[role] = struct{}{}
		}
	}
	return func(c *gin.Context) {
		access, ok := ProjectAccessFromGin(c)
		if !ok || !access.Role.IsValid() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": "project_access_denied",
				"msg":  "无权访问该项目",
			})
			return
		}
		if _, allowed := allowlist[access.Role]; !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": "project_role_denied",
				"msg":  "项目权限不足",
			})
			return
		}
		c.Next()
	}
}

func writeProjectArchiveError(
	c *gin.Context,
	response *middleware.ResponseHelper,
	err error,
) {
	switch {
	case errors.Is(err, services.ErrProjectPublicID):
		response.BadRequest(c, "项目公共 ID 无效")
	case errors.Is(err, services.ErrProjectAccessDenied):
		response.Forbidden(c, "无权归档该项目")
	case errors.Is(err, services.ErrProjectNotFound):
		response.NotFound(c, "项目不存在")
	case errors.Is(err, services.ErrProjectInactive):
		response.Error(c, http.StatusConflict, "项目当前状态不允许归档")
	case errors.Is(err, services.ErrDefaultProjectArchive):
		response.Error(c, http.StatusConflict, "系统默认项目不能归档")
	default:
		response.InternalServerError(c, "项目归档失败")
	}
}

func writeProjectError(
	c *gin.Context,
	response *middleware.ResponseHelper,
	err error,
) {
	switch {
	case errors.Is(err, services.ErrProjectGovernanceQuery),
		errors.Is(err, services.ErrDirectoryListQuery),
		errors.Is(err, services.ErrBusinessUnitPublicID),
		errors.Is(err, services.ErrInitialProjectAdministrator):
		response.BadRequest(c, "项目治理参数无效")
	case errors.Is(err, services.ErrProjectAccessDenied):
		response.Forbidden(c, "无权访问该项目")
	case errors.Is(err, services.ErrProjectNotFound),
		errors.Is(err, services.ErrQueueNotFound),
		errors.Is(err, services.ErrProjectMembershipNotFound),
		errors.Is(err, services.ErrProjectMembershipUser):
		response.NotFound(c, "项目、队列或成员不存在")
	case errors.Is(err, services.ErrProjectInactive):
		response.Forbidden(c, "项目已停用")
	case errors.Is(err, services.ErrLastProjectAdministrator):
		response.Error(c, http.StatusConflict, "项目必须保留至少一名有效管理员")
	case errors.Is(err, services.ErrProjectMembershipVersionConflict):
		response.Error(
			c,
			http.StatusConflict,
			"成员关系已被其他操作更新，请刷新成员列表后重试",
		)
	default:
		response.InternalServerError(c, "项目操作失败")
	}
}

func writeProjectScopeResolutionError(
	c *gin.Context,
	response *middleware.ResponseHelper,
	err error,
) {
	switch {
	case errors.Is(err, services.ErrProjectAccessDenied),
		errors.Is(err, services.ErrProjectInactive):
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": "project_access_revoked",
			"msg":  "当前项目访问权限已失效",
		})
	default:
		writeProjectError(c, response, err)
	}
}

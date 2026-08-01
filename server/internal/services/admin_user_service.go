package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ProjectMembershipDeactivatedEventType = "io.chronodesk.project.membership.deactivated.v1"
	UserAccessRevokedEventType            = "io.chronodesk.user.access-revoked.v1"
	ProjectAccessRevokedEventType         = "io.chronodesk.project.access-revoked.v1"
	adminUserAccessEventDestinationID     = "access-revocation"
)

var (
	ErrAdminUserIdentityConflict       = errors.New("admin user identity already exists")
	ErrLastActivePlatformAdministrator = errors.New(
		"last active platform administrator must be preserved",
	)
	ErrAdminUserAccessEventWriter = errors.New(
		"admin user access revocation event writer is unavailable",
	)
	ErrInvalidAdminUserAvatar = errors.New(
		"admin user avatar must be an uploaded local avatar path",
	)
)

// AdminUserService 管理员用户管理服务
type AdminUserService struct {
	db         *gorm.DB
	events     AdminUserAccessEventAppender
	eventScope models.ProjectScope
}

type AdminUserAccessEventAppender interface {
	AppendDomainEventTx(
		context.Context,
		*gorm.DB,
		DomainEventInput,
		[]OutboxTarget,
	) (*models.DomainEvent, error)
}

// NewAdminUserService 创建管理员用户管理服务
func NewAdminUserService(db *gorm.DB) *AdminUserService {
	return &AdminUserService{
		db: db,
	}
}

// NewAdminUserServiceWithAccessRevocationOutbox creates the production
// platform-user command boundary. The active DEFAULT project is a durable RLS
// envelope for organization-wide access-control events; the event payload's
// user ID remains the global revocation target.
func NewAdminUserServiceWithAccessRevocationOutbox(
	db *gorm.DB,
	events AdminUserAccessEventAppender,
) (*AdminUserService, error) {
	if db == nil {
		return nil, errors.New("admin user database is required")
	}
	if events == nil {
		return nil, ErrAdminUserAccessEventWriter
	}
	var projects []models.Project
	if err := db.
		Select("id", "organization_id").
		Where(
			"key = ? AND status = ?",
			models.ProjectKey("DEFAULT"),
			models.ProjectStatusActive,
		).
		Order("organization_id ASC, id ASC").
		Limit(2).
		Find(&projects).Error; err != nil {
		return nil, fmt.Errorf(
			"resolve admin user access-event project: %w",
			err,
		)
	}
	if len(projects) != 1 {
		return nil, fmt.Errorf(
			"admin user access events require one active DEFAULT project, found %d",
			len(projects),
		)
	}
	scope := projects[0].Scope()
	if err := scope.Validate(); err != nil {
		return nil, fmt.Errorf(
			"admin user access-event project scope: %w",
			err,
		)
	}
	return &AdminUserService{
		db:         db,
		events:     events,
		eventScope: scope,
	}, nil
}

// UserListRequest 用户列表请求
type UserListRequest struct {
	Page         int                  `form:"page" binding:"omitempty,min=1"`
	PageSize     int                  `form:"page_size" binding:"omitempty,min=1,max=100"`
	PlatformRole *models.PlatformRole `form:"platform_role" binding:"omitempty,oneof=platform_admin security_auditor emergency_operator member"`
	Status       *models.UserStatus   `form:"status" binding:"omitempty,oneof=active inactive suspended deleted"`
	Search       string               `form:"search" binding:"omitempty,max=100"`
	OrderBy      string               `form:"order_by" binding:"omitempty,oneof=id username email created_at updated_at last_login_at"`
	Order        string               `form:"order" binding:"omitempty,oneof=asc desc"`
}

// UserListResponse 用户列表响应
type UserListResponse struct {
	Items    []*models.UserResponse `json:"items"`
	Total    int64                  `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
	Pages    int                    `json:"pages"`
}

var ErrInvalidAdminUserListQuery = errors.New(
	"invalid admin user list query",
)

// GetUserList 获取用户列表
func (s *AdminUserService) GetUserList(ctx context.Context, req *UserListRequest) (*UserListResponse, error) {
	if s == nil || s.db == nil || req == nil {
		return nil, ErrInvalidAdminUserListQuery
	}
	if req.PlatformRole != nil && !req.PlatformRole.IsValid() {
		return nil, ErrInvalidAdminUserListQuery
	}
	// 设置默认值
	if req.Page == 0 {
		req.Page = 1
	}
	if req.PageSize == 0 {
		req.PageSize = 25
	}
	if req.OrderBy == "" {
		req.OrderBy = "created_at"
	}
	if req.Order == "" {
		req.Order = "desc"
	}
	if req.Page < 1 || req.PageSize < 1 || req.PageSize > 100 ||
		req.Page > math.MaxInt/req.PageSize ||
		len([]rune(req.Search)) > 100 {
		return nil, ErrInvalidAdminUserListQuery
	}
	switch req.OrderBy {
	case "id", "username", "email", "created_at", "updated_at",
		"last_login_at":
	default:
		return nil, ErrInvalidAdminUserListQuery
	}
	if req.Order != "asc" && req.Order != "desc" {
		return nil, ErrInvalidAdminUserListQuery
	}

	query := s.db.WithContext(ctx).Model(&models.User{})

	// 过滤条件
	if req.PlatformRole != nil {
		query = query.Where("platform_role = ?", *req.PlatformRole)
	}

	if req.Status != nil {
		query = query.Where("status = ?", *req.Status)
	}

	// 搜索条件（用户名、邮箱、姓名）
	if req.Search != "" {
		search := "%" + req.Search + "%"
		query = query.Where(
			"username LIKE ? OR email LIKE ? OR first_name LIKE ? OR last_name LIKE ? OR display_name LIKE ?",
			search, search, search, search, search,
		)
	}

	// 统计总数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("failed to count users: %w", err)
	}

	// 排序列由固定映射构造，避免任何调用方绕过 HTTP binding 后把原始
	// 请求字符串带入 SQL。
	orderColumn := clause.Column{Name: "created_at"}
	switch req.OrderBy {
	case "id":
		orderColumn.Name = "id"
	case "username":
		orderColumn.Name = "username"
	case "email":
		orderColumn.Name = "email"
	case "updated_at":
		orderColumn.Name = "updated_at"
	case "last_login_at":
		orderColumn.Name = "last_login_at"
	case "created_at":
		orderColumn.Name = "created_at"
	}
	orderColumns := []clause.OrderByColumn{{
		Column: orderColumn,
		Desc:   !strings.EqualFold(req.Order, "asc"),
	}}
	if orderColumn.Name != "id" {
		orderColumns = append(orderColumns, clause.OrderByColumn{
			Column: clause.Column{Name: "id"},
			Desc:   !strings.EqualFold(req.Order, "asc"),
		})
	}
	query = query.Clauses(clause.OrderBy{Columns: orderColumns})

	// 分页
	offset := (req.Page - 1) * req.PageSize
	query = query.Offset(offset).Limit(req.PageSize)

	// 查询数据
	var users []models.User
	if err := query.Preload("Manager").Find(&users).Error; err != nil {
		return nil, fmt.Errorf("failed to get users: %w", err)
	}

	// 转换为响应格式
	items := make([]*models.UserResponse, len(users))
	for i, user := range users {
		items[i] = user.ToResponse()
	}

	// 计算总页数
	pages := int(total) / req.PageSize
	if int(total)%req.PageSize != 0 {
		pages++
	}

	return &UserListResponse{
		Items:    items,
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
		Pages:    pages,
	}, nil
}

// GetUserByID 根据ID获取用户详细信息
func (s *AdminUserService) GetUserByID(ctx context.Context, userID uint) (*models.User, error) {
	var user models.User
	err := s.db.WithContext(ctx).
		Preload("Manager").
		First(&user, userID).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &user, nil
}

// CreateUser 创建新用户
func (s *AdminUserService) CreateUser(ctx context.Context, req *models.UserCreateRequest) (*models.User, error) {
	if !req.PlatformRole.IsValid() {
		return nil, fmt.Errorf("invalid platform role")
	}
	// 用户名和邮箱是长期审计身份，软删除后仍不得复用。Unscoped 与数据库
	// 唯一索引保持同一语义，避免“活动查询未找到、INSERT 却返回 23505”。
	var existingUser models.User
	err := s.db.WithContext(ctx).
		Unscoped().
		Where("username = ?", req.Username).
		First(&existingUser).Error
	if err == nil {
		return nil, fmt.Errorf("%w: username", ErrAdminUserIdentityConflict)
	}
	if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed to check username: %w", err)
	}

	err = s.db.WithContext(ctx).
		Unscoped().
		Where("email = ?", req.Email).
		First(&existingUser).Error
	if err == nil {
		return nil, fmt.Errorf("%w: email", ErrAdminUserIdentityConflict)
	}
	if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed to check email: %w", err)
	}

	// 加密密码
	hashedPassword, err := s.hashPassword(req.Password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	// 创建用户
	user := &models.User{
		Username:     req.Username,
		Email:        req.Email,
		Phone:        req.Phone,
		PasswordHash: hashedPassword,
		FirstName:    req.FirstName,
		LastName:     req.LastName,
		DisplayName:  req.DisplayName,
		PlatformRole: req.PlatformRole,
		Status:       models.UserStatusActive, // 管理员创建的用户默认激活
		Department:   req.Department,
		JobTitle:     req.JobTitle,
		ManagerID:    req.ManagerID,
		Timezone:     "Asia/Shanghai",
		Language:     "zh-CN",
	}

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		return tx.Create(userProfileProjection(user)).Error
	}); err != nil {
		// 预检查不能替代数据库约束；并发创建时由唯一索引裁决，并映射为
		// 稳定冲突而不是把数据库细节泄漏成 500。
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, fmt.Errorf("%w: concurrent create", ErrAdminUserIdentityConflict)
		}
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	// 重新加载用户信息（包含关联数据）
	err = s.db.WithContext(ctx).Preload("Manager").First(user, user.ID).Error
	if err != nil {
		return nil, fmt.Errorf("failed to reload user: %w", err)
	}

	return user, nil
}

// UpdateUser 更新用户信息
func (s *AdminUserService) UpdateUser(
	ctx context.Context,
	actor models.ActorRef,
	userID uint,
	req *models.UserUpdateRequest,
) (*models.User, error) {
	if err := validateAdminUserHumanActor(actor); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, errors.New("user update request is required")
	}
	if req.PlatformRole != nil && !req.PlatformRole.IsValid() {
		return nil, fmt.Errorf("invalid platform role")
	}
	var updated *models.User
	err := s.withMutationTransaction(ctx, actor, func(commandContext context.Context, tx *gorm.DB) error {
		if err := lockPlatformAdministratorInvariant(tx); err != nil {
			return err
		}
		var err error
		updated, err = s.updateUserOnDB(
			commandContext,
			tx,
			userID,
			req,
		)
		if err != nil {
			return err
		}
		if req.Status == nil || *req.Status == models.UserStatusActive {
			return nil
		}
		return s.appendAccessRevokedEventTx(
			commandContext,
			tx,
			updated,
		)
	})
	return updated, err
}

func (s *AdminUserService) withMutationTransaction(
	ctx context.Context,
	actor models.ActorRef,
	run func(context.Context, *gorm.DB) error,
) error {
	if s == nil || s.db == nil || run == nil {
		return errors.New("admin user mutation is unavailable")
	}
	if err := validateAdminUserHumanActor(actor); err != nil {
		return err
	}
	if s.events == nil {
		return transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
			return run(ctx, tx)
		})
	}
	if scopeddb.HasTransaction(ctx) {
		return errors.New(
			"admin user mutation must own its access-event transaction",
		)
	}
	operationContext, err := WithOperationContext(
		ctx,
		OperationContext{
			Scope:  s.eventScope,
			Actor:  actor,
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		return fmt.Errorf("bind admin user access-event context: %w", err)
	}
	return scopeddb.WithProjectScopeContextTransaction(
		operationContext,
		s.db,
		s.eventScope,
		func(scopedContext context.Context) error {
			return transactionForContext(
				scopedContext,
				s.db,
				func(tx *gorm.DB) error {
					return run(scopedContext, tx)
				},
			)
		},
	)
}

func (s *AdminUserService) appendAccessRevokedEventTx(
	ctx context.Context,
	tx *gorm.DB,
	user *models.User,
) error {
	if s == nil || s.events == nil {
		return nil
	}
	if tx == nil || user == nil || user.ID == 0 {
		return ErrAdminUserAccessEventWriter
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil || operation.Scope != s.eventScope ||
		validateAdminUserHumanActor(operation.Actor) != nil {
		return ErrAdminUserAccessEventWriter
	}
	_, err = s.events.AppendDomainEventTx(
		ctx,
		tx,
		DomainEventInput{
			Type:    UserAccessRevokedEventType,
			Subject: fmt.Sprintf("user/%d", user.ID),
			Data: map[string]any{
				"user_id": user.ID,
				"status":  user.Status,
			},
			Scope: s.eventScope,
			Actor: operation.Actor,
		},
		[]OutboxTarget{{
			Type:        "event_stream",
			ID:          adminUserAccessEventDestinationID,
			MaxAttempts: 8,
		}},
	)
	if err != nil {
		return fmt.Errorf("append admin user access-revoked event: %w", err)
	}
	return nil
}

func (s *AdminUserService) updateUserOnDB(
	ctx context.Context,
	db *gorm.DB,
	userID uint,
	req *models.UserUpdateRequest,
) (*models.User, error) {
	if req.PlatformRole != nil && !req.PlatformRole.IsValid() {
		return nil, fmt.Errorf("invalid platform role")
	}
	user := &models.User{}
	if err := db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(user, userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to find user: %w", err)
	}
	if req.Avatar != nil {
		currentAvatar := user.Avatar
		var authoritativeProfile models.UserProfile
		profileErr := db.WithContext(ctx).
			Select("id", "avatar").
			Where("user_id = ?", userID).
			First(&authoritativeProfile).Error
		if profileErr == nil {
			currentAvatar = authoritativeProfile.Avatar
		} else if !errors.Is(profileErr, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed to load authoritative avatar: %w", profileErr)
		}
		if *req.Avatar != "" && *req.Avatar != currentAvatar {
			return nil, ErrInvalidAdminUserAvatar
		}
	}

	if user.PlatformRole == models.PlatformRolePlatformAdmin {
		targetRole := user.PlatformRole
		if req.PlatformRole != nil {
			targetRole = *req.PlatformRole
		}
		targetStatus := user.Status
		if req.Status != nil {
			targetStatus = *req.Status
		}
		if targetRole != models.PlatformRolePlatformAdmin ||
			targetStatus != models.UserStatusActive {
			var activeAdminCount int64
			if err := db.WithContext(ctx).
				Model(&models.User{}).
				Where(
					"platform_role = ? AND id != ? AND status = ?",
					models.PlatformRolePlatformAdmin,
					userID,
					models.UserStatusActive,
				).
				Count(&activeAdminCount).Error; err != nil {
				return nil, fmt.Errorf("failed to count active admins: %w", err)
			}
			if activeAdminCount == 0 {
				return nil, fmt.Errorf(
					"%w: cannot demote or deactivate",
					ErrLastActivePlatformAdministrator,
				)
			}
		}
	}

	// 构建更新数据
	updates := make(map[string]interface{})

	if req.Email != nil {
		// 审计保留的软删除身份同样占用邮箱。
		var count int64
		if err := db.WithContext(ctx).
			Unscoped().
			Model(&models.User{}).
			Where("email = ? AND id != ?", *req.Email, userID).
			Count(&count).Error; err != nil {
			return nil, fmt.Errorf("failed to check email: %w", err)
		}
		if count > 0 {
			return nil, fmt.Errorf("%w: email", ErrAdminUserIdentityConflict)
		}
		updates["email"] = *req.Email
		updates["email_verified"] = false // 邮箱变更需要重新验证
	}

	if req.Phone != nil {
		updates["phone"] = *req.Phone
		updates["phone_verified"] = false // 手机变更需要重新验证
	}

	if req.FirstName != nil {
		updates["first_name"] = *req.FirstName
	}

	if req.LastName != nil {
		updates["last_name"] = *req.LastName
	}

	if req.DisplayName != nil {
		updates["display_name"] = *req.DisplayName
	}

	if req.Avatar != nil {
		updates["avatar"] = *req.Avatar
	}

	if req.Timezone != nil {
		updates["timezone"] = *req.Timezone
	}

	if req.Language != nil {
		updates["language"] = *req.Language
	}

	if req.PlatformRole != nil {
		updates["platform_role"] = *req.PlatformRole
	}

	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.EmailVerified != nil {
		updates["email_verified"] = *req.EmailVerified
	}

	if req.Department != nil {
		updates["department"] = *req.Department
	}

	if req.JobTitle != nil {
		updates["job_title"] = *req.JobTitle
	}

	if req.ManagerID != nil {
		updates["manager_id"] = *req.ManagerID
	}

	// 执行更新
	if len(updates) > 0 {
		if err := db.WithContext(ctx).Model(user).Updates(updates).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return nil, fmt.Errorf("%w: concurrent update", ErrAdminUserIdentityConflict)
			}
			return nil, fmt.Errorf("failed to update user: %w", err)
		}
	}

	if req.Avatar != nil || req.Phone != nil ||
		req.Timezone != nil || req.Language != nil {
		var profile models.UserProfile
		err := db.WithContext(ctx).
			Where("user_id = ?", user.ID).
			First(&profile).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			profile = *userProfileProjection(user)
		} else if err != nil {
			return nil, fmt.Errorf("failed to load user profile: %w", err)
		}
		if req.Avatar != nil {
			profile.Avatar = *req.Avatar
		}
		if req.Phone != nil {
			profile.Phone = *req.Phone
		}
		if req.Timezone != nil {
			profile.Timezone = *req.Timezone
		}
		if req.Language != nil {
			profile.Language = *req.Language
		}
		if err := db.WithContext(ctx).Save(&profile).Error; err != nil {
			return nil, fmt.Errorf("failed to update user profile: %w", err)
		}
	}

	// 重新加载用户信息
	err := db.WithContext(ctx).Preload("Manager").First(user, user.ID).Error
	if err != nil {
		return nil, fmt.Errorf("failed to reload user: %w", err)
	}

	return user, nil
}

func userProfileProjection(user *models.User) *models.UserProfile {
	timezone := user.Timezone
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	language := user.Language
	if language == "" {
		language = "zh-CN"
	}
	return &models.UserProfile{
		UserID:   user.ID,
		Avatar:   user.Avatar,
		Phone:    user.Phone,
		Timezone: timezone,
		Language: language,
	}
}

// ResetUserPassword 重置用户密码
func (s *AdminUserService) ResetUserPassword(ctx context.Context, userID uint, newPassword string) error {
	user := &models.User{}
	if err := s.db.WithContext(ctx).First(user, userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("user not found")
		}
		return fmt.Errorf("failed to find user: %w", err)
	}

	// 加密新密码
	hashedPassword, err := s.hashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	now := time.Now()
	// 密码更新与会话撤销必须处于同一事务中。否则管理员重置密码后，
	// 旧刷新令牌仍可能签发新的访问令牌。
	updates := map[string]interface{}{
		"password_hash":     hashedPassword,
		"password_reset_at": now,
		"login_attempts":    0,   // 重置登录尝试次数
		"locked_until":      nil, // 解除锁定
	}

	return transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		if err := tx.Model(user).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to reset password: %w", err)
		}
		if err := tx.Table("refresh_tokens").
			Where("user_id = ? AND revoked = ?", userID, false).
			Updates(map[string]interface{}{
				"revoked":    true,
				"revoked_at": now,
			}).Error; err != nil {
			return fmt.Errorf("failed to revoke user refresh tokens: %w", err)
		}
		if err := tx.Model(&models.LoginHistory{}).
			Where("user_id = ? AND is_active = ?", userID, true).
			Updates(map[string]interface{}{
				"is_active":        false,
				"logout_time":      now,
				"last_activity_at": now,
				"login_status":     models.LoginStatusExpired,
				"failure_reason":   "password_reset",
			}).Error; err != nil {
			return fmt.Errorf("failed to close user login sessions: %w", err)
		}
		return nil
	})
}

// DeleteUser 删除用户（软删除）
func (s *AdminUserService) DeleteUser(
	ctx context.Context,
	actor models.ActorRef,
	userID uint,
) error {
	if err := validateAdminUserHumanActor(actor); err != nil {
		return err
	}
	return s.withMutationTransaction(
		ctx,
		actor,
		func(commandContext context.Context, tx *gorm.DB) error {
			if err := lockPlatformAdministratorInvariant(tx); err != nil {
				return err
			}
			user := &models.User{}
			if err := tx.WithContext(commandContext).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				First(user, userID).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					return fmt.Errorf("user not found")
				}
				return fmt.Errorf("failed to find user: %w", err)
			}
			if user.PlatformRole == models.PlatformRolePlatformAdmin {
				var adminCount int64
				if err := tx.WithContext(ctx).Model(&models.User{}).
					Where(
						"platform_role = ? AND id != ? AND status = ?",
						models.PlatformRolePlatformAdmin,
						userID,
						models.UserStatusActive,
					).
					Count(&adminCount).Error; err != nil {
					return fmt.Errorf("failed to count active admins: %w", err)
				}
				if adminCount == 0 {
					return fmt.Errorf(
						"%w: cannot delete",
						ErrLastActivePlatformAdministrator,
					)
				}
			}

			now := time.Now()
			// 先使账号与所有长期会话失效，再软删除主体。认证/登录记录保留
			// 作为审计证据，因此不能通过级联物理删除解决外键冲突。
			if err := tx.Model(user).Update("status", models.UserStatusDeleted).Error; err != nil {
				return fmt.Errorf("failed to disable user before deletion: %w", err)
			}
			user.Status = models.UserStatusDeleted
			if err := tx.Table("refresh_tokens").
				Where("user_id = ? AND revoked = ?", userID, false).
				Updates(map[string]interface{}{
					"revoked":    true,
					"revoked_at": now,
				}).Error; err != nil {
				return fmt.Errorf("failed to revoke user refresh tokens: %w", err)
			}
			if err := tx.Model(&models.LoginHistory{}).
				Where("user_id = ? AND is_active = ?", userID, true).
				Updates(map[string]interface{}{
					"is_active":   false,
					"logout_time": now,
				}).Error; err != nil {
				return fmt.Errorf("failed to close user login sessions: %w", err)
			}
			if err := tx.Model(&models.OTPTrustedDevice{}).
				Where("user_id = ? AND revoked = ?", userID, false).
				Update("revoked", true).Error; err != nil {
				return fmt.Errorf("failed to revoke trusted devices: %w", err)
			}
			if err := tx.Delete(user).Error; err != nil {
				return fmt.Errorf("failed to soft delete user: %w", err)
			}
			return s.appendAccessRevokedEventTx(
				commandContext,
				tx,
				user,
			)
		},
	)
}

func validateAdminUserHumanActor(actor models.ActorRef) error {
	if actor.Type != models.ActorTypeHuman || actor.Validate() != nil {
		return errors.New("authenticated human actor is required")
	}
	userID, err := strconv.ParseUint(actor.ID, 10, strconv.IntSize)
	if err != nil ||
		userID == 0 ||
		models.HumanActor(uint(userID)) != actor {
		return errors.New("authenticated human actor is invalid")
	}
	return nil
}

func lockPlatformAdministratorInvariant(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("platform administrator transaction is required")
	}
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	if err := tx.Exec(
		"SELECT pg_advisory_xact_lock(hashtext(?))",
		"chronodesk-platform-administrator-invariant",
	).Error; err != nil {
		return fmt.Errorf("lock platform administrator invariant: %w", err)
	}
	return nil
}

// GetUserStats 获取用户统计信息
func (s *AdminUserService) GetUserStats(ctx context.Context) (*UserStatsResponse, error) {
	stats := &UserStatsResponse{}

	// 总用户数
	var totalUsers int64
	if err := s.db.WithContext(ctx).Model(&models.User{}).Count(&totalUsers).Error; err != nil {
		return nil, fmt.Errorf("failed to count total users: %w", err)
	}
	stats.TotalUsers = totalUsers

	// 活跃用户数
	var activeUsers int64
	if err := s.db.WithContext(ctx).Model(&models.User{}).
		Where("status = ?", models.UserStatusActive).Count(&activeUsers).Error; err != nil {
		return nil, fmt.Errorf("failed to count active users: %w", err)
	}
	stats.ActiveUsers = activeUsers

	// 按平台职责统计
	type PlatformRoleCount struct {
		PlatformRole models.PlatformRole `json:"platform_role"`
		Count        int64               `json:"count"`
	}

	var roleCounts []PlatformRoleCount
	if err := s.db.WithContext(ctx).Model(&models.User{}).
		Select("platform_role, COUNT(*) as count").
		Group("platform_role").
		Scan(&roleCounts).Error; err != nil {
		return nil, fmt.Errorf("failed to count users by platform role: %w", err)
	}

	stats.UsersByPlatformRole = make(map[string]int64)
	for _, rc := range roleCounts {
		stats.UsersByPlatformRole[string(rc.PlatformRole)] = rc.Count
	}

	// 最近7天新用户
	weekAgo := time.Now().AddDate(0, 0, -7)
	var newUsersThisWeek int64
	if err := s.db.WithContext(ctx).Model(&models.User{}).
		Where("created_at >= ?", weekAgo).Count(&newUsersThisWeek).Error; err != nil {
		return nil, fmt.Errorf("failed to count new users this week: %w", err)
	}
	stats.NewUsersThisWeek = newUsersThisWeek

	return stats, nil
}

// UserStatsResponse 用户统计响应
type UserStatsResponse struct {
	TotalUsers          int64            `json:"total_users"`
	ActiveUsers         int64            `json:"active_users"`
	UsersByPlatformRole map[string]int64 `json:"users_by_platform_role"`
	NewUsersThisWeek    int64            `json:"new_users_this_week"`
}

// hashPassword 加密密码
func (s *AdminUserService) hashPassword(password string) (string, error) {
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashedBytes), nil
}

package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gongdan-system/internal/models"
)

// AdminUserService 管理员用户管理服务
type AdminUserService struct {
	db *gorm.DB
}

// NewAdminUserService 创建管理员用户管理服务
func NewAdminUserService(db *gorm.DB) *AdminUserService {
	return &AdminUserService{
		db: db,
	}
}

// UserListRequest 用户列表请求
type UserListRequest struct {
	Page     int                `form:"page" binding:"omitempty,min=1"`
	PageSize int                `form:"page_size" binding:"omitempty,min=1,max=100"`
	Role     *models.UserRole   `form:"role" binding:"omitempty,oneof=admin agent customer supervisor"`
	Status   *models.UserStatus `form:"status" binding:"omitempty,oneof=active inactive suspended deleted"`
	Search   string             `form:"search" binding:"omitempty,max=100"`
	OrderBy  string             `form:"order_by" binding:"omitempty,oneof=id username email created_at updated_at last_login_at"`
	Order    string             `form:"order" binding:"omitempty,oneof=asc desc"`
}

// UserListResponse 用户列表响应
type UserListResponse struct {
	Items    []*models.UserResponse `json:"items"`
	Total    int64                  `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
	Pages    int                    `json:"pages"`
}

// GetUserList 获取用户列表
func (s *AdminUserService) GetUserList(ctx context.Context, req *UserListRequest) (*UserListResponse, error) {
	// 设置默认值
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 20
	}
	if req.OrderBy == "" {
		req.OrderBy = "created_at"
	}
	if req.Order == "" {
		req.Order = "desc"
	}

	query := s.db.WithContext(ctx).Model(&models.User{})

	// 过滤条件
	if req.Role != nil {
		query = query.Where("role = ?", *req.Role)
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

	// 排序
	orderClause := fmt.Sprintf("%s %s", req.OrderBy, strings.ToUpper(req.Order))
	query = query.Order(orderClause)

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
	// 检查用户名是否已存在
	var existingUser models.User
	err := s.db.WithContext(ctx).Where("username = ?", req.Username).First(&existingUser).Error
	if err == nil {
		return nil, fmt.Errorf("username already exists")
	}
	if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed to check username: %w", err)
	}

	// 检查邮箱是否已存在
	err = s.db.WithContext(ctx).Where("email = ?", req.Email).First(&existingUser).Error
	if err == nil {
		return nil, fmt.Errorf("email already exists")
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
		Role:         req.Role,
		Status:       models.UserStatusActive, // 管理员创建的用户默认激活
		Department:   req.Department,
		JobTitle:     req.JobTitle,
		ManagerID:    req.ManagerID,
		Timezone:     "Asia/Shanghai",
		Language:     "zh-CN",
	}

	if err := s.db.WithContext(ctx).Create(user).Error; err != nil {
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
func (s *AdminUserService) UpdateUser(ctx context.Context, userID uint, req *models.UserUpdateRequest) (*models.User, error) {
	user := &models.User{}
	if err := s.db.WithContext(ctx).First(user, userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to find user: %w", err)
	}

	// 构建更新数据
	updates := make(map[string]interface{})

	if req.Email != nil {
		// 检查邮箱是否被其他用户使用
		var count int64
		s.db.WithContext(ctx).Model(&models.User{}).
			Where("email = ? AND id != ?", *req.Email, userID).Count(&count)
		if count > 0 {
			return nil, fmt.Errorf("email already exists")
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

	if req.Role != nil {
		updates["role"] = *req.Role
	}

	if req.Status != nil {
		updates["status"] = *req.Status
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
		if err := s.db.WithContext(ctx).Model(user).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("failed to update user: %w", err)
		}
	}

	// 重新加载用户信息
	err := s.db.WithContext(ctx).Preload("Manager").First(user, user.ID).Error
	if err != nil {
		return nil, fmt.Errorf("failed to reload user: %w", err)
	}

	return user, nil
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

	// 更新密码
	updates := map[string]interface{}{
		"password_hash":    hashedPassword,
		"password_reset_at": time.Now(),
		"login_attempts":   0,      // 重置登录尝试次数
		"locked_until":     nil,    // 解除锁定
	}

	if err := s.db.WithContext(ctx).Model(user).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to reset password: %w", err)
	}

	return nil
}

// DeleteUser 删除用户（软删除）
func (s *AdminUserService) DeleteUser(ctx context.Context, userID uint) error {
	user := &models.User{}
	if err := s.db.WithContext(ctx).First(user, userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("user not found")
		}
		return fmt.Errorf("failed to find user: %w", err)
	}

	// 检查是否为系统管理员
	if user.Role == models.RoleAdmin {
		// 检查是否为最后一个管理员
		var adminCount int64
		s.db.WithContext(ctx).Model(&models.User{}).
			Where("role = ? AND id != ? AND status = ?", models.RoleAdmin, userID, models.UserStatusActive).
			Count(&adminCount)
		
		if adminCount == 0 {
			return fmt.Errorf("cannot delete the last admin user")
		}
	}

	// 执行软删除
	if err := s.db.WithContext(ctx).Delete(user).Error; err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	return nil
}

// ToggleUserStatus 切换用户状态（启用/禁用）
func (s *AdminUserService) ToggleUserStatus(ctx context.Context, userID uint) (*models.User, error) {
	user := &models.User{}
	if err := s.db.WithContext(ctx).First(user, userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to find user: %w", err)
	}

	// 切换状态
	var newStatus models.UserStatus
	if user.Status == models.UserStatusActive {
		newStatus = models.UserStatusSuspended
	} else if user.Status == models.UserStatusSuspended {
		newStatus = models.UserStatusActive
	} else {
		return nil, fmt.Errorf("cannot toggle status for user with status: %s", user.Status)
	}

	// 检查是否为最后一个活跃管理员
	if user.Role == models.RoleAdmin && newStatus == models.UserStatusSuspended {
		var activeAdminCount int64
		s.db.WithContext(ctx).Model(&models.User{}).
			Where("role = ? AND id != ? AND status = ?", models.RoleAdmin, userID, models.UserStatusActive).
			Count(&activeAdminCount)
		
		if activeAdminCount == 0 {
			return nil, fmt.Errorf("cannot suspend the last active admin user")
		}
	}

	// 更新状态
	if err := s.db.WithContext(ctx).Model(user).Update("status", newStatus).Error; err != nil {
		return nil, fmt.Errorf("failed to update user status: %w", err)
	}

	// 重新加载用户信息
	err := s.db.WithContext(ctx).Preload("Manager").First(user, user.ID).Error
	if err != nil {
		return nil, fmt.Errorf("failed to reload user: %w", err)
	}

	return user, nil
}

// BatchDeleteUsers 批量删除用户
func (s *AdminUserService) BatchDeleteUsers(ctx context.Context, userIDs []uint) error {
	if len(userIDs) == 0 {
		return fmt.Errorf("no user IDs provided")
	}

	// 查询要删除的用户
	var users []models.User
	if err := s.db.WithContext(ctx).Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return fmt.Errorf("failed to find users: %w", err)
	}

	if len(users) == 0 {
		return fmt.Errorf("no users found")
	}

	// 检查管理员用户
	adminIDs := make([]uint, 0)
	for _, user := range users {
		if user.Role == models.RoleAdmin {
			adminIDs = append(adminIDs, user.ID)
		}
	}

	// 如果包含管理员，检查是否会删除所有管理员
	if len(adminIDs) > 0 {
		var totalAdminCount int64
		s.db.WithContext(ctx).Model(&models.User{}).
			Where("role = ? AND status = ?", models.RoleAdmin, models.UserStatusActive).
			Count(&totalAdminCount)
		
		if int64(len(adminIDs)) >= totalAdminCount {
			return fmt.Errorf("cannot delete all admin users")
		}
	}

	// 执行批量删除
	if err := s.db.WithContext(ctx).Delete(&models.User{}, userIDs).Error; err != nil {
		return fmt.Errorf("failed to batch delete users: %w", err)
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

	// 按角色统计
	type RoleCount struct {
		Role  models.UserRole `json:"role"`
		Count int64           `json:"count"`
	}

	var roleCounts []RoleCount
	if err := s.db.WithContext(ctx).Model(&models.User{}).
		Select("role, COUNT(*) as count").
		Group("role").
		Scan(&roleCounts).Error; err != nil {
		return nil, fmt.Errorf("failed to count users by role: %w", err)
	}

	stats.UsersByRole = make(map[string]int64)
	for _, rc := range roleCounts {
		stats.UsersByRole[string(rc.Role)] = rc.Count
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
	TotalUsers        int64            `json:"total_users"`
	ActiveUsers       int64            `json:"active_users"`
	UsersByRole       map[string]int64 `json:"users_by_role"`
	NewUsersThisWeek  int64            `json:"new_users_this_week"`
}

// hashPassword 加密密码
func (s *AdminUserService) hashPassword(password string) (string, error) {
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashedBytes), nil
}
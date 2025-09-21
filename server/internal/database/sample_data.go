package database

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"gorm.io/gorm"
	"gongdan-system/internal/models"
)

// SampleDataGenerator 示例数据生成器
type SampleDataGenerator struct {
	db    *gorm.DB
	users map[string]*models.User // 缓存用户，便于关联
}

// NewSampleDataGenerator 创建示例数据生成器
func NewSampleDataGenerator(db *gorm.DB) *SampleDataGenerator {
	return &SampleDataGenerator{
		db:    db,
		users: make(map[string]*models.User),
	}
}

// GenerateAllSampleData 生成完整的示例数据集
func (g *SampleDataGenerator) GenerateAllSampleData() error {
	log.Println("🚀 开始生成示例数据...")

	// 按依赖顺序生成数据
	if err := g.createSampleUsers(); err != nil {
		return fmt.Errorf("创建示例用户失败: %w", err)
	}

	if err := g.createSampleCategories(); err != nil {
		return fmt.Errorf("创建示例分类失败: %w", err)
	}

	if err := g.createSampleTickets(); err != nil {
		return fmt.Errorf("创建示例工单失败: %w", err)
	}

	if err := g.createSampleComments(); err != nil {
		return fmt.Errorf("创建示例评论失败: %w", err)
	}

	if err := g.createSampleHistory(); err != nil {
		return fmt.Errorf("创建示例历史失败: %w", err)
	}

	log.Println("✅ 示例数据生成完成")
	return nil
}

// createSampleUsers 创建示例用户
func (g *SampleDataGenerator) createSampleUsers() error {
	log.Println("👥 创建示例用户...")

	// 检查是否已有示例用户
	var count int64
	if err := g.db.Model(&models.User{}).Where("email LIKE ?", "%@sample.com").Count(&count).Error; err != nil {
		return err
	}
	
	if count > 0 {
		log.Printf("示例用户已存在，跳过创建 (%d个)", count)
		return g.loadExistingUsers()
	}

	sampleUsers := []struct {
		Username    string
		Email       string
		FirstName   string
		LastName    string
		Role        models.UserRole
		Department  string
		JobTitle    string
	}{
		{"tech_support", "support@sample.com", "张", "技术", models.RoleAgent, "技术部", "技术支持专员"},
		{"customer_service", "service@sample.com", "李", "客服", models.RoleAgent, "客服部", "客户服务专员"},
		{"project_manager", "pm@sample.com", "王", "经理", models.RoleAgent, "项目部", "项目经理"},
		{"demo_user1", "user1@sample.com", "陈", "用户", models.RoleCustomer, "销售部", "销售专员"},
		{"demo_user2", "user2@sample.com", "刘", "测试", models.RoleCustomer, "研发部", "测试工程师"},
		{"demo_user3", "user3@sample.com", "赵", "设计", models.RoleCustomer, "设计部", "UI设计师"},
	}

	// 统一密码哈希 (DemoPass123!)
	passwordHash := "$2a$12$rMd8z8Z7Zq4wX.yN3jCNNO2rT1qJxJ5Lw4E3X9dP8kL6vN8pQ2eGu"

	for _, userData := range sampleUsers {
		user := &models.User{
			Username:      userData.Username,
			Email:         userData.Email,
			FirstName:     userData.FirstName,
			LastName:      userData.LastName,
			Role:          userData.Role,
			Status:        models.UserStatusActive,
			EmailVerified: true,
			Department:    userData.Department,
			JobTitle:      userData.JobTitle,
			PasswordHash:  passwordHash,
		}

		if err := g.db.Create(user).Error; err != nil {
			return fmt.Errorf("创建用户 %s 失败: %w", userData.Email, err)
		}

		// 缓存用户便于后续使用
		g.users[userData.Email] = user
		log.Printf("✓ 创建用户: %s (%s)", user.Email, user.Role)
	}

	log.Printf("✅ 创建了 %d 个示例用户", len(sampleUsers))
	return nil
}

// loadExistingUsers 加载已存在的用户到缓存
func (g *SampleDataGenerator) loadExistingUsers() error {
	var users []models.User
	if err := g.db.Where("email LIKE ?", "%@sample.com").Find(&users).Error; err != nil {
		return err
	}

	for _, user := range users {
		userCopy := user // 避免循环变量指针问题
		g.users[user.Email] = &userCopy
	}

	log.Printf("✓ 加载了 %d 个已存在的示例用户", len(users))
	return nil
}

// createSampleCategories 创建示例分类
func (g *SampleDataGenerator) createSampleCategories() error {
	log.Println("📁 创建示例分类...")

	// 检查是否已有示例分类
	var count int64
	if err := g.db.Model(&models.Category{}).Where("name LIKE ?", "%示例%").Count(&count).Error; err != nil {
		return err
	}

	if count > 0 {
		log.Printf("示例分类已存在，跳过创建 (%d个)", count)
		return nil
	}

	// 获取管理员用户作为创建者
	var adminUser models.User
	if err := g.db.Where("role = ?", models.RoleAdmin).First(&adminUser).Error; err != nil {
		return fmt.Errorf("获取管理员用户失败: %w", err)
	}

	categories := []models.Category{
		{
			Name:        "系统故障示例",
			Slug:        "system-issues-sample",
			Description: "系统相关故障和问题示例分类",
			Type:        models.CategoryTypeIncident,
			Status:      models.CategoryStatusActive,
			IsPublic:    true,
			SortOrder:   10,
			CreatedBy:   adminUser.ID,
		},
		{
			Name:        "功能请求示例",
			Slug:        "feature-requests-sample", 
			Description: "新功能和改进请求示例分类",
			Type:        models.CategoryTypeRequest,
			Status:      models.CategoryStatusActive,
			IsPublic:    true,
			SortOrder:   20,
			CreatedBy:   adminUser.ID,
		},
		{
			Name:        "用户咨询示例",
			Slug:        "user-inquiry-sample",
			Description: "用户咨询和使用问题示例分类",
			Type:        models.CategoryTypeSupport,
			Status:      models.CategoryStatusActive,
			IsPublic:    true,
			SortOrder:   30,
			CreatedBy:   adminUser.ID,
		},
	}

	for _, category := range categories {
		if err := g.db.Create(&category).Error; err != nil {
			return fmt.Errorf("创建分类 %s 失败: %w", category.Name, err)
		}
		log.Printf("✓ 创建分类: %s", category.Name)
	}

	log.Printf("✅ 创建了 %d 个示例分类", len(categories))
	return nil
}

// createSampleTickets 创建示例工单
func (g *SampleDataGenerator) createSampleTickets() error {
	log.Println("🎫 创建示例工单...")

	// 检查是否已有示例工单
	var count int64
	if err := g.db.Model(&models.Ticket{}).Where("title LIKE ?", "%示例%").Count(&count).Error; err != nil {
		return err
	}

	if count > 0 {
		log.Printf("示例工单已存在，跳过创建 (%d个)", count)
		return nil
	}

	// 获取分类
	var categories []models.Category
	if err := g.db.Where("name LIKE ?", "%示例%").Find(&categories).Error; err != nil {
		return fmt.Errorf("获取示例分类失败: %w", err)
	}

	if len(categories) == 0 {
		return fmt.Errorf("未找到示例分类")
	}

	// 工单模板数据
	ticketTemplates := []struct {
		Title       string
		Description string
		Type        models.TicketType
		Priority    models.TicketPriority
		Status      models.TicketStatus
		Source      models.TicketSource
		CreatorKey  string
		AssigneeKey string
	}{
		{
			Title:       "登录页面无法访问示例工单",
			Description: "用户反馈登录页面出现500错误，无法正常访问系统。请技术团队尽快处理。\n\n**错误现象：**\n1. 访问登录页面时显示500内部服务器错误\n2. 浏览器控制台显示JavaScript报错\n3. 影响所有用户正常使用\n\n**紧急程度：**高\n**预期解决时间：**2小时内",
			Type:        models.TicketTypeIncident,
			Priority:    models.TicketPriorityHigh,
			Status:      models.TicketStatusInProgress,
			Source:      models.TicketSourceWeb,
			CreatorKey:  "user1@sample.com",
			AssigneeKey: "support@sample.com",
		},
		{
			Title:       "新增数据导出功能请求示例",
			Description: "希望系统能够支持数据导出功能，具体需求如下：\n\n**功能需求：**\n1. 支持Excel格式导出\n2. 可选择导出字段\n3. 支持按日期范围筛选\n4. 支持大数据量分批导出\n\n**业务场景：**\n月度报告制作时需要导出统计数据进行分析\n\n**期望完成时间：**下个版本发布",
			Type:        models.TicketTypeRequest,
			Priority:    models.TicketPriorityNormal,
			Status:      models.TicketStatusOpen,
			Source:      models.TicketSourceWeb,
			CreatorKey:  "user2@sample.com",
			AssigneeKey: "",
		},
		{
			Title:       "系统性能优化问题示例",
			Description: "最近发现系统响应速度明显变慢，特别是在数据量大的页面。\n\n**具体表现：**\n1. 页面加载时间超过10秒\n2. 查询大量数据时浏览器卡顿\n3. 用户体验明显下降\n\n**影响范围：**\n- 数据报表页面\n- 历史记录查询\n- 文件上传功能\n\n**建议：**\n希望技术团队进行性能分析和优化",
			Type:        models.TicketTypeProblem,
			Priority:    models.TicketPriorityHigh,
			Status:      models.TicketStatusPending,
			Source:      models.TicketSourceWeb,
			CreatorKey:  "user3@sample.com",
			AssigneeKey: "pm@sample.com",
		},
		{
			Title:       "用户权限配置咨询示例",
			Description: "请问如何为新员工配置系统权限？\n\n**具体问题：**\n1. 新员工需要哪些基础权限？\n2. 如何设置部门级别的数据访问权限？\n3. 权限配置后多久生效？\n\n**用户信息：**\n- 部门：销售部\n- 职位：销售专员\n- 需要访问：客户信息、订单数据\n\n谢谢！",
			Type:        models.TicketTypeConsultation,
			Priority:    models.TicketPriorityLow,
			Status:      models.TicketStatusResolved,
			Source:      models.TicketSourceEmail,
			CreatorKey:  "user1@sample.com",
			AssigneeKey: "service@sample.com",
		},
		{
			Title:       "移动端适配改进建议示例",
			Description: "移动端使用体验需要改进，建议如下：\n\n**问题描述：**\n1. 按钮在手机上太小，难以点击\n2. 表格在小屏幕上显示不完整\n3. 文字大小需要自适应\n\n**改进建议：**\n1. 增大触控按钮尺寸\n2. 优化表格横向滚动\n3. 实现响应式字体大小\n4. 添加手势操作支持\n\n**优先级：**中等\n**影响用户：**所有移动端用户",
			Type:        models.TicketTypeChange,
			Priority:    models.TicketPriorityNormal,
			Status:      models.TicketStatusOpen,
			Source:      models.TicketSourceMobile,
			CreatorKey:  "user2@sample.com",
			AssigneeKey: "",
		},
		{
			Title:       "数据同步异常投诉示例",
			Description: "数据同步功能存在严重问题，导致业务受影响！\n\n**问题详情：**\n1. 昨天提交的数据今天还未同步\n2. 部分数据同步后丢失\n3. 同步状态显示不准确\n\n**业务影响：**\n- 无法获取最新的业务数据\n- 报告制作延误\n- 客户投诉增加\n\n**要求：**\n1. 立即修复数据同步问题\n2. 恢复丢失的数据\n3. 提供问题说明和预防措施\n\n**紧急程度：**非常高！",
			Type:        models.TicketTypeComplaint,
			Priority:    models.TicketPriorityCritical,
			Status:      models.TicketStatusInProgress,
			Source:      models.TicketSourcePhone,
			CreatorKey:  "user3@sample.com",
			AssigneeKey: "support@sample.com",
		},
	}

	rand.Seed(time.Now().UnixNano())

	for i, template := range ticketTemplates {
		// 随机选择分类
		categoryIndex := rand.Intn(len(categories))
		category := categories[categoryIndex]

		// 获取创建者
		creator, exists := g.users[template.CreatorKey]
		if !exists {
			return fmt.Errorf("未找到创建者用户: %s", template.CreatorKey)
		}

		// 生成工单编号
		ticketNumber := fmt.Sprintf("TK%s%03d", time.Now().Format("20060102"), i+1)

		ticket := models.Ticket{
			TicketNumber: ticketNumber,
			Title:        template.Title,
			Description:  template.Description,
			Type:         template.Type,
			Priority:     template.Priority,
			Status:       template.Status,
			Source:       template.Source,
			CreatedByID:  creator.ID,
			CategoryID:   &category.ID,
			CreatedAt:    time.Now().Add(-time.Duration(rand.Intn(72)) * time.Hour), // 随机过去3天内创建
		}

		// 设置分配者（如果指定）
		if template.AssigneeKey != "" {
			if assignee, exists := g.users[template.AssigneeKey]; exists {
				ticket.AssignedToID = &assignee.ID
			}
		}

		// 为已解决的工单设置解决时间
		if ticket.Status == models.TicketStatusResolved {
			resolvedTime := ticket.CreatedAt.Add(time.Duration(rand.Intn(48)) * time.Hour)
			ticket.ResolvedAt = &resolvedTime
		}

		if err := g.db.Create(&ticket).Error; err != nil {
			return fmt.Errorf("创建工单失败: %w", err)
		}

		log.Printf("✓ 创建工单: %s (%s)", ticket.TicketNumber, ticket.Title)
	}

	log.Printf("✅ 创建了 %d 个示例工单", len(ticketTemplates))
	return nil
}

// createSampleComments 创建示例评论
func (g *SampleDataGenerator) createSampleComments() error {
	log.Println("💬 创建示例评论...")

	// 获取所有示例工单
	var tickets []models.Ticket
	if err := g.db.Where("title LIKE ?", "%示例%").Find(&tickets).Error; err != nil {
		return fmt.Errorf("获取示例工单失败: %w", err)
	}

	if len(tickets) == 0 {
		log.Println("未找到示例工单，跳过评论创建")
		return nil
	}

	// 检查是否已有示例评论
	var count int64
	if err := g.db.Model(&models.TicketComment{}).Where("content LIKE ?", "%这是一个示例%").Count(&count).Error; err != nil {
		return err
	}

	if count > 0 {
		log.Printf("示例评论已存在，跳过创建 (%d个)", count)
		return nil
	}

	commentTemplates := []string{
		"这是一个示例评论：我已经收到这个工单，正在分析问题。预计今天下午给出解决方案。",
		"这是一个示例评论：经过初步排查，问题可能与数据库连接有关，需要进一步调试。",
		"这是一个示例评论：已联系相关技术人员，将在2小时内提供详细的解决方案。",
		"这是一个示例评论：问题已定位并修复，请用户验证功能是否正常。",
		"这是一个示例评论：感谢反馈！这个建议很有价值，我们会在下个版本中考虑实现。",
		"这是一个示例评论：已按照用户要求完成配置，相关权限已生效，请查收。",
	}

	rand.Seed(time.Now().UnixNano())
	createdComments := 0

	for _, ticket := range tickets {
		// 为每个工单随机创建1-3个评论
		commentCount := rand.Intn(3) + 1
		
		for i := 0; i < commentCount; i++ {
			// 随机选择评论模板
			template := commentTemplates[rand.Intn(len(commentTemplates))]
			
			// 随机选择评论者（优先分配者，否则随机选择）
			var commenterID uint
			if ticket.AssignedToID != nil {
				commenterID = *ticket.AssignedToID
			} else {
				// 随机选择一个用户
				userEmails := make([]string, 0, len(g.users))
				for email := range g.users {
					userEmails = append(userEmails, email)
				}
				randomEmail := userEmails[rand.Intn(len(userEmails))]
				commenterID = g.users[randomEmail].ID
			}

			comment := models.TicketComment{
				TicketID:  ticket.ID,
				UserID:    commenterID,
				Content:   template,
				Type:      models.CommentTypePublic,
				CreatedAt: ticket.CreatedAt.Add(time.Duration(rand.Intn(24*int(time.Since(ticket.CreatedAt).Hours()))) * time.Hour),
			}

			if err := g.db.Create(&comment).Error; err != nil {
				log.Printf("创建评论失败: %v", err)
				continue
			}

			createdComments++
		}
	}

	log.Printf("✅ 创建了 %d 个示例评论", createdComments)
	return nil
}

// createSampleHistory 创建示例历史记录
func (g *SampleDataGenerator) createSampleHistory() error {
	log.Println("📝 创建示例历史记录...")

	// 获取所有示例工单
	var tickets []models.Ticket
	if err := g.db.Where("title LIKE ?", "%示例%").Find(&tickets).Error; err != nil {
		return fmt.Errorf("获取示例工单失败: %w", err)
	}

	if len(tickets) == 0 {
		log.Println("未找到示例工单，跳过历史记录创建")
		return nil
	}

	// 检查是否已有示例历史记录
	var count int64
	if err := g.db.Model(&models.TicketHistory{}).Where("description LIKE ?", "%示例%").Count(&count).Error; err != nil {
		return err
	}

	if count > 0 {
		log.Printf("示例历史记录已存在，跳过创建 (%d个)", count)
		return nil
	}

	createdHistory := 0

	for _, ticket := range tickets {
		// 为每个工单创建基础历史记录
		histories := []models.TicketHistory{
			{
				TicketID:    ticket.ID,
				UserID:      &ticket.CreatedByID,
				Action:      models.HistoryActionCreate,
				Description: "工单创建示例记录",
				CreatedAt:   ticket.CreatedAt,
			},
		}

		// 如果工单有分配者，添加分配记录
		if ticket.AssignedToID != nil {
			histories = append(histories, models.TicketHistory{
				TicketID:    ticket.ID,
				UserID:      ticket.AssignedToID,
				Action:      models.HistoryActionAssign,
				Description: "工单分配示例记录：已分配给技术支持团队",
				CreatedAt:   ticket.CreatedAt.Add(30 * time.Minute),
			})
		}

		// 如果工单状态不是开放，添加状态变更记录
		if ticket.Status != models.TicketStatusOpen {
			var actionType models.HistoryAction
			var description string

			switch ticket.Status {
			case models.TicketStatusInProgress:
				actionType = models.HistoryActionUpdate
				description = "状态变更示例记录：工单状态更改为处理中"
			case models.TicketStatusPending:
				actionType = models.HistoryActionUpdate
				description = "状态变更示例记录：工单状态更改为等待中，需要用户提供更多信息"
			case models.TicketStatusResolved:
				actionType = models.HistoryActionResolve
				description = "问题解决示例记录：工单已解决，解决方案已实施"
			case models.TicketStatusClosed:
				actionType = models.HistoryActionClose
				description = "工单关闭示例记录：工单已关闭，用户确认问题已解决"
			}

			var userID *uint = &ticket.CreatedByID
			if ticket.AssignedToID != nil {
				userID = ticket.AssignedToID
			}

			histories = append(histories, models.TicketHistory{
				TicketID:    ticket.ID,
				UserID:      userID,
				Action:      actionType,
				Description: description,
				CreatedAt:   ticket.CreatedAt.Add(2 * time.Hour),
			})
		}

		// 批量创建历史记录
		for _, history := range histories {
			if err := g.db.Create(&history).Error; err != nil {
				log.Printf("创建历史记录失败: %v", err)
				continue
			}
			createdHistory++
		}
	}

	log.Printf("✅ 创建了 %d 个示例历史记录", createdHistory)
	return nil
}

// CleanupSampleData 清理示例数据（开发调试用）
func (g *SampleDataGenerator) CleanupSampleData() error {
	log.Println("🗑️ 清理示例数据...")

	// 按相反顺序删除数据以维护外键约束
	tables := []struct {
		model interface{}
		where string
	}{
		{&models.TicketHistory{}, "description LIKE '%示例%'"},
		{&models.TicketComment{}, "content LIKE '%这是一个示例%'"},
		{&models.Ticket{}, "title LIKE '%示例%'"},
		{&models.Category{}, "name LIKE '%示例%'"},
		{&models.User{}, "email LIKE '%@sample.com'"},
	}

	for _, table := range tables {
		result := g.db.Where(table.where).Delete(table.model)
		if result.Error != nil {
			return fmt.Errorf("清理数据失败: %w", result.Error)
		}
		if result.RowsAffected > 0 {
			log.Printf("✓ 清理了 %d 条记录", result.RowsAffected)
		}
	}

	log.Println("✅ 示例数据清理完成")
	return nil
}
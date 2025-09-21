package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/joho/godotenv"
	"gongdan-system/internal/auth"
	"gongdan-system/internal/config"
	"gongdan-system/internal/database"
	"gongdan-system/internal/handlers"
	"gongdan-system/internal/middleware"
	"gongdan-system/internal/services"
	websocketPkg "gongdan-system/internal/websocket"
)

// @title 工单管理系统 API
// @version 1.0
// @description 基于 Go Gin 的工单管理系统 RESTful API
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://www.swagger.io/support
// @contact.email support@swagger.io

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @host localhost:8081
// @BasePath /api

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.

// ginAdapter 将认证处理器适配为Gin处理器
func ginAdapter(handler func(auth.HTTPContext)) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := auth.NewGinHTTPContext(c)
		handler(ctx)
	}
}

func testRedis() {
	// 加载环境变量
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found")
	}

	// 首先尝试TCP连接
	fmt.Println("=== Testing TCP Connection ===")
	testRedisTCP()

	// 然后尝试HTTP REST API
	fmt.Println("\n=== Testing HTTP REST API ===")
	testRedisHTTP()
}

func testRedisTCP() {
	// 获取Redis URL
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		log.Fatal("REDIS_URL environment variable not set")
	}

	fmt.Printf("Testing Redis TCP connection with URL: %s\n", redisURL)

	// 解析Redis URL
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	}

	// 如果是rediss://协议，配置TLS
	if strings.HasPrefix(redisURL, "rediss://") {
		opt.TLSConfig = &tls.Config{
			ServerName: strings.Split(opt.Addr, ":")[0],
			MinVersion: tls.VersionTLS12,
		}
	}

	fmt.Printf("Parsed Redis options:\n")
	fmt.Printf("  Addr: %s\n", opt.Addr)
	fmt.Printf("  DB: %d\n", opt.DB)
	fmt.Printf("  TLSConfig: %v\n", opt.TLSConfig != nil)

	// 创建Redis客户端
	rdb := redis.NewClient(opt)
	defer rdb.Close()

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Println("\nTesting Redis TCP connection...")

	// 执行PING命令
	pong, err := rdb.Ping(ctx).Result()
	if err != nil {
		fmt.Printf("❌ Redis TCP PING failed: %v\n", err)
		return
	}

	fmt.Printf("✅ Redis TCP PING successful: %s\n", pong)
}

func testRedisHTTP() {
	// 获取REST API配置
	restURL := os.Getenv("KV_REST_API_URL")
	restToken := os.Getenv("KV_REST_API_TOKEN")

	if restURL == "" || restToken == "" {
		fmt.Println("❌ KV_REST_API_URL or KV_REST_API_TOKEN not set")
		return
	}

	fmt.Printf("Testing Redis HTTP REST API: %s\n", restURL)

	// 测试PING命令
	pingURL := restURL + "/ping"
	req, err := http.NewRequest("GET", pingURL, nil)
	if err != nil {
		fmt.Printf("❌ Failed to create request: %v\n", err)
		return
	}

	req.Header.Set("Authorization", "Bearer "+restToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ HTTP request failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("❌ Failed to read response: %v\n", err)
		return
	}

	fmt.Printf("HTTP Status: %d\n", resp.StatusCode)
	fmt.Printf("Response: %s\n", string(body))

	if resp.StatusCode == 200 {
		fmt.Println("✅ Redis HTTP REST API PING successful!")

		// 测试SET/GET操作
		testRedisHTTPOperations(restURL, restToken)
	} else {
		fmt.Printf("❌ Redis HTTP REST API PING failed with status %d\n", resp.StatusCode)
	}
}

func testRedisHTTPOperations(baseURL, token string) {
	fmt.Println("\nTesting SET/GET operations...")

	// 测试SET操作
	setURL := baseURL + "/set/test_key/test_value"
	req, err := http.NewRequest("GET", setURL, nil)
	if err != nil {
		fmt.Printf("❌ Failed to create SET request: %v\n", err)
		return
	}

	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ SET request failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("❌ Failed to read SET response: %v\n", err)
		return
	}

	fmt.Printf("SET Response: %s\n", string(body))

	// 测试GET操作
	getURL := baseURL + "/get/test_key"
	req, err = http.NewRequest("GET", getURL, nil)
	if err != nil {
		fmt.Printf("❌ Failed to create GET request: %v\n", err)
		return
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("❌ GET request failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("❌ Failed to read GET response: %v\n", err)
		return
	}

	fmt.Printf("GET Response: %s\n", string(body))
	fmt.Println("✅ Redis HTTP REST API operations completed!")
}

func main() {

	// 加载环境变量
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found")
	}

	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}

	// 设置 Gin 模式
	if cfg.Server.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	// 初始化数据库
	db, err := database.New(cfg)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	// 可选的数据库迁移（通过环境变量控制）
	if os.Getenv("AUTO_MIGRATE") == "true" {
		log.Println("Starting database migration...")
		if err := database.RunMigrations(db.DB); err != nil {
			log.Fatal("Failed to run database migrations:", err)
		}
		log.Println("Database migration completed")
	} else {
		log.Println("Skipping database migration (set AUTO_MIGRATE=true to enable)")
	}

	// 初始化认证模块
	authModule, err := auth.NewAuthModule(db.DB)
	if err != nil {
		log.Fatal("Failed to initialize auth module:", err)
	}

	// 初始化清理服务和调度器
	log.Println("Initializing cleanup service and scheduler...")
	schedulerService := services.NewSchedulerService(db.DB)

	// 启动调度器（在后台运行）
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Scheduler panic: %v", r)
			}
		}()
		schedulerService.Start()
	}()

	// 优雅关闭处理
	defer func() {
		log.Println("Shutting down scheduler...")
		schedulerService.Stop()
	}()

	// 创建 Gin 路由器
	r := gin.New()

	// 设置中间件配置
	var middlewareConfig *middleware.MiddlewareConfig
	if cfg.Server.Environment == "production" {
		middlewareConfig = middleware.ProductionMiddlewareConfig()
	} else {
		middlewareConfig = middleware.DevelopmentMiddlewareConfig()
	}

	// 设置JWT密钥
	if cfg.JWT.Secret != "" {
		middlewareConfig.JWT.SecretKey = cfg.JWT.Secret
	}

	// 应用基础中间件（不包含JWT）
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// 添加CORS中间件
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Length, Content-Type, Authorization, Accept, Accept-Encoding, Accept-Language, X-Requested-With, X-CSRF-Token, X-Request-ID")
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Expose-Headers", "Content-Length, X-Request-ID, X-Response-Time")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	})

	// 健康检查端点
	r.GET("/healthz", func(c *gin.Context) {
		// 检查数据库连接
		dbStatus := "ok"
		if err := db.HealthCheck(); err != nil {
			dbStatus = "error: " + err.Error()
		}

		c.JSON(http.StatusOK, gin.H{
			"status":   "ok",
			"message":  "Ticket System API is running",
			"version":  "1.0.0",
			"database": dbStatus,
		})
	})

	// API 路由组
	api := r.Group("/api")
	{
		api.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"message": "pong",
			})
		})

		// 健康检查端点（公开）
		analyticsHandler := handlers.NewAnalyticsHandler(db.DB)
		api.GET("/health", analyticsHandler.GetHealthCheck)

		// 认证路由
		authGroup := api.Group("/auth")
		{
			authGroup.POST("/register", ginAdapter(authModule.Handler.Register))
			authGroup.POST("/login", ginAdapter(authModule.Handler.Login))
			authGroup.POST("/logout", ginAdapter(authModule.Handler.Logout))
			authGroup.POST("/refresh", ginAdapter(authModule.Handler.RefreshToken))
			authGroup.POST("/forgot-password", ginAdapter(authModule.Handler.ForgotPassword))
			authGroup.POST("/reset-password", ginAdapter(authModule.Handler.ResetPassword))
			authGroup.POST("/verify-email", ginAdapter(authModule.Handler.VerifyEmail))
			authGroup.POST("/resend-verification", ginAdapter(authModule.Handler.ResendVerification))

			// 需要认证的路由
			authenticated := authGroup.Group("/")
			authenticated.Use(ginAdapter(authModule.Handler.RequireAuth))
			{
				authenticated.GET("/me", ginAdapter(authModule.Handler.GetProfile))
				authenticated.GET("/profile", ginAdapter(authModule.Handler.GetProfile))
				authenticated.PUT("/profile", ginAdapter(authModule.Handler.UpdateProfile))
				authenticated.POST("/change-password", ginAdapter(authModule.Handler.ChangePassword))
				authenticated.POST("/enable-otp", ginAdapter(authModule.Handler.EnableOTP))
				authenticated.POST("/disable-otp", ginAdapter(authModule.Handler.DisableOTP))
				authenticated.POST("/verify-otp", ginAdapter(authModule.Handler.VerifyOTP))
				authenticated.POST("/otp/backup-codes", ginAdapter(authModule.Handler.GenerateBackupCodes))
			}
		}

		// 工单路由
		tickets := api.Group("/tickets")
		{
			// 创建工单服务和处理器
			ticketService := services.NewTicketService(db.DB)
			ticketHandler := handlers.NewTicketHandler(ticketService)
			workflowHandler := handlers.NewTicketWorkflowHandler(ticketService)

			// 所有工单路由都需要认证
			tickets.Use(ginAdapter(authModule.Handler.RequireAuth))

			// 基础工单CRUD路由
			tickets.GET("", ticketHandler.GetTickets)          // 获取工单列表
			tickets.GET("/:id", ticketHandler.GetTicket)       // 获取单个工单
			tickets.POST("", ticketHandler.CreateTicket)       // 创建工单
			tickets.PUT("/:id", ticketHandler.UpdateTicket)    // 更新工单
			tickets.DELETE("/:id", ticketHandler.DeleteTicket) // 删除工单

			// 工作流相关路由
			tickets.POST("/:id/assign", workflowHandler.AssignTicket)       // 分配工单
			tickets.POST("/:id/transfer", workflowHandler.TransferTicket)   // 转移工单
			tickets.POST("/:id/escalate", workflowHandler.EscalateTicket)   // 升级工单
			tickets.POST("/:id/status", workflowHandler.UpdateTicketStatus) // 更新状态
			tickets.GET("/:id/history", workflowHandler.GetTicketHistory)   // 获取工单历史

			// 统计和特殊查询路由
			tickets.GET("/stats", workflowHandler.GetTicketStats)             // 获取工单统计
			tickets.GET("/my-tickets", workflowHandler.GetMyTickets)          // 获取我的工单
			tickets.GET("/unassigned", workflowHandler.GetUnassignedTickets)  // 获取未分配工单
			tickets.GET("/overdue", workflowHandler.GetOverdueTickets)        // 获取逾期工单
			tickets.GET("/sla-breach", workflowHandler.GetSLABreachedTickets) // 获取SLA违约工单

			// 批量操作路由
			tickets.POST("/bulk-assign", workflowHandler.BulkAssignTickets) // 批量分配
			tickets.POST("/bulk-status", workflowHandler.BulkUpdateStatus)  // 批量状态更新
			tickets.POST("/bulk-update", ticketHandler.BulkUpdateTickets)   // 原有批量更新
		}

		// 邮箱配置路由
		emailConfigService := services.NewEmailConfigService(db.DB)
		emailConfigHandler := handlers.NewEmailConfigHandler(emailConfigService)

		// 公开的邮箱状态查询端点
		api.GET("/email-status", emailConfigHandler.GetEmailStatus)

		// 用户个人中心路由（需要认证）
		userService := services.NewUserService(db.DB)
		trustedDeviceService := services.NewTrustedDeviceService(db.DB)
		userHandler := handlers.NewUserHandler(userService, trustedDeviceService)
		adminAuditService := services.NewAdminAuditService(db.DB)
		adminAuditHandler := handlers.NewAdminAuditHandler(adminAuditService)

		user := api.Group("/user")
		user.Use(ginAdapter(authModule.Handler.RequireAuth))
		{
			user.GET("/profile", userHandler.GetProfile)
			user.PUT("/profile", userHandler.UpdateProfile)
			user.PUT("/password", userHandler.ChangePassword)
			user.GET("/login-history", userHandler.GetLoginHistory)
			user.GET("/stats", userHandler.GetStats)
			user.POST("/avatar", userHandler.UploadAvatar)
			user.DELETE("/login-history/:id", userHandler.DeleteLoginSession)
			user.GET("/trusted-devices", userHandler.GetTrustedDevices)
			user.DELETE("/trusted-devices/:id", userHandler.RevokeTrustedDevice)
		}

		// 管理员路由（需要认证和管理员权限）
		admin := api.Group("/admin")
		admin.Use(ginAdapter(authModule.Handler.RequireAuth))
		admin.Use(ginAdapter(authModule.Handler.RequireRole(auth.RoleAdmin)))
		admin.Use(middleware.LogAdminOperation(adminAuditService))
		{
			// 邮箱配置管理
			admin.GET("/email-config", emailConfigHandler.GetEmailConfig)
			admin.PUT("/email-config", emailConfigHandler.UpdateEmailConfig)
			admin.POST("/email-config/test", emailConfigHandler.TestEmailConnection)

			// 管理员用户管理路由
			adminUserService := services.NewAdminUserService(db.DB)
			adminUserHandler := handlers.NewAdminUserHandler(adminUserService)

			// 用户管理路由
			admin.GET("/users", adminUserHandler.GetUserList)
			admin.GET("/users/stats", adminUserHandler.GetUserStats)
			admin.GET("/users/:id", adminUserHandler.GetUser)
			admin.POST("/users", adminUserHandler.CreateUser)
			admin.PUT("/users/:id", adminUserHandler.UpdateUser)
			admin.DELETE("/users/:id", adminUserHandler.DeleteUser)
			admin.POST("/users/:id/reset-password", adminUserHandler.ResetUserPassword)
			admin.POST("/users/:id/toggle-status", adminUserHandler.ToggleUserStatus)
			admin.POST("/users/batch-delete", adminUserHandler.BatchDeleteUsers)
			admin.GET("/audit-logs", adminAuditHandler.GetAuditLogs)

			// 系统配置和清理管理路由
			systemHandler := handlers.NewSystemHandler(db.DB)
			systemHandler.RegisterRoutes(admin)

			// 系统全局配置管理路由
			configHandler := handlers.NewConfigHandler(db.DB)
			configs := admin.Group("/configs")
			{
				configs.GET("", configHandler.GetAllConfigs)                     // 获取所有配置
				configs.GET("/:key", configHandler.GetConfig)                    // 获取单个配置
				configs.POST("", configHandler.CreateConfig)                     // 创建配置
				configs.PUT("/:key", configHandler.UpdateConfig)                 // 更新配置
				configs.DELETE("/:key", configHandler.DeleteConfig)              // 删除配置
				configs.PUT("/batch", configHandler.BatchUpdateConfigs)          // 批量更新配置
				configs.GET("/security-policy", configHandler.GetSecurityPolicy) // 获取安全策略
				configs.GET("/export", configHandler.ExportConfigs)              // 导出配置
				configs.POST("/import", configHandler.ImportConfigs)             // 导入配置
				configs.POST("/cache/clear", configHandler.ClearCache)           // 清空缓存
				configs.GET("/cache/stats", configHandler.GetCacheStats)         // 缓存统计
				configs.POST("/init", configHandler.InitDefaultConfigs)          // 初始化默认配置
			}

			// 系统监控统计管理路由
			analyticsHandler := handlers.NewAnalyticsHandler(db.DB)
			analytics := admin.Group("/analytics")
			{
				analytics.GET("/system", analyticsHandler.GetSystemStats)       // 获取系统运行状态
				analytics.GET("/business", analyticsHandler.GetBusinessStats)   // 获取业务数据统计
				analytics.GET("/dashboard", analyticsHandler.GetDashboardStats) // 获取仪表板综合统计
				analytics.GET("/timerange", analyticsHandler.GetTimeRangeStats) // 获取指定时间范围统计
				analytics.GET("/export", analyticsHandler.ExportStats)          // 导出统计数据
				analytics.GET("/realtime", analyticsHandler.GetRealtimeMetrics) // 获取实时指标
			}

			// FE008 自动化流程管理路由
			automationHandler := handlers.NewAutomationHandler(db.DB, schedulerService)
			automation := admin.Group("/automation")
			{
				// 自动化规则管理
				rules := automation.Group("/rules")
				{
					rules.POST("", automationHandler.CreateRule)            // 创建自动化规则
					rules.GET("", automationHandler.GetRules)               // 获取规则列表
					rules.GET("/:id", automationHandler.GetRule)            // 获取规则详情
					rules.PUT("/:id", automationHandler.UpdateRule)         // 更新规则
					rules.DELETE("/:id", automationHandler.DeleteRule)      // 删除规则
					rules.GET("/:id/stats", automationHandler.GetRuleStats) // 获取规则统计
				}

				// 执行日志查询
				automation.GET("/logs", automationHandler.GetExecutionLogs) // 获取执行日志

				// SLA配置管理
				sla := automation.Group("/sla")
				{
					sla.POST("", automationHandler.CreateSLAConfig) // 创建SLA配置
					sla.GET("", automationHandler.GetSLAConfigs)    // 获取SLA配置列表
				}

				// 工单模板管理
				templates := automation.Group("/templates")
				{
					templates.POST("", automationHandler.CreateTemplate) // 创建工单模板
					templates.GET("", automationHandler.GetTemplates)    // 获取模板列表
					templates.GET("/:id", automationHandler.GetTemplate) // 获取模板详情
				}

				// 快速回复管理
				quickReplies := automation.Group("/quick-replies")
				{
					quickReplies.POST("", automationHandler.CreateQuickReply)      // 创建快速回复
					quickReplies.GET("", automationHandler.GetQuickReplies)        // 获取快速回复列表
					quickReplies.POST("/:id/use", automationHandler.UseQuickReply) // 使用快速回复
				}

				// 批量操作
				batch := automation.Group("/batch")
				{
					batch.POST("/update", automationHandler.BatchUpdateTickets) // 批量更新工单
					batch.POST("/assign", automationHandler.BatchAssignTickets) // 批量分配工单
				}
			}
		}

		// 通知系统服务和处理器
		notificationService := services.NewNotificationService(db.DB)

		// 邮件配置服务 (使用前面已声明的变量)
		// emailConfigService already declared above

		// 邮件通知服务
		emailNotificationService := services.NewEmailNotificationService(db.DB, emailConfigService, notificationService)

		// 将邮件通知服务注入到通知服务中
		notificationService.SetEmailNotificationService(emailNotificationService)

		notificationHandler := handlers.NewNotificationHandler(notificationService)

		// 初始化 WebSocket Hub 和 WebSocket 通知服务
		wsHub := websocketPkg.NewHub()
		wsNotificationService := websocketPkg.NewNotificationWebSocketService(wsHub)

		// 启动 WebSocket Hub（在后台运行）
		go wsHub.Run()

		// 设置全局WebSocket通知服务以供hook使用
		websocketPkg.SetGlobalNotificationService(wsNotificationService)

		// 管理员通知管理路由
		admin.POST("/notifications", notificationHandler.CreateNotification) // 创建通知（管理员）

		// 通知系统路由（需要认证）
		notifications := api.Group("/notifications")
		notifications.Use(ginAdapter(authModule.Handler.RequireAuth))
		{
			notifications.GET("", notificationHandler.GetNotifications)                          // 获取通知列表
			notifications.PUT("/:id/read", notificationHandler.MarkAsRead)                       // 标记单个通知为已读
			notifications.PUT("/read-all", notificationHandler.MarkAllAsRead)                    // 标记所有通知为已读
			notifications.GET("/unread-count", notificationHandler.GetUnreadCount)               // 获取未读通知数量
			notifications.GET("/preferences", notificationHandler.GetNotificationPreferences)    // 获取通知偏好设置
			notifications.PUT("/preferences", notificationHandler.UpdateNotificationPreferences) // 更新通知偏好设置
		}

		// WebSocket 连接端点 (需要认证)
		api.GET("/ws", ginAdapter(authModule.Handler.RequireAuth), func(c *gin.Context) {
			userIDVal, exists := c.Get("user_id")
			if !exists {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "user not authenticated"})
				return
			}

			// TODO: 将用户ID传入WebSocket服务以区分连接
			if userID, ok := userIDVal.(uint); ok {
				c.Set("ws_user_id", userID)
			}

			websocketPkg.ServeWS(wsHub, c)
		})

		// Webhook管理路由（需要管理员权限）
		webhooks := api.Group("/webhooks")
		webhooks.Use(ginAdapter(authModule.Handler.RequireAuth))
		webhooks.Use(ginAdapter(authModule.Handler.RequireRole(auth.RoleAdmin)))
		webhooks.Use(middleware.LogAdminOperation(adminAuditService))
		{
			// 创建Webhook处理器
			webhookHandler := handlers.NewWebhookHandler(db.DB)

			// Webhook配置管理路由
			webhooks.GET("", webhookHandler.ListWebhooks)              // 获取webhook列表
			webhooks.POST("", webhookHandler.CreateWebhook)            // 创建webhook
			webhooks.GET("/:id", webhookHandler.GetWebhook)            // 获取webhook详情
			webhooks.PUT("/:id", webhookHandler.UpdateWebhook)         // 更新webhook
			webhooks.DELETE("/:id", webhookHandler.DeleteWebhook)      // 删除webhook
			webhooks.POST("/:id/test", webhookHandler.TestWebhook)     // 测试webhook
			webhooks.GET("/:id/logs", webhookHandler.GetWebhookLogs)   // 获取webhook日志
			webhooks.GET("/:id/stats", webhookHandler.GetWebhookStats) // 获取webhook统计
		}

		// Redis 连接测试端点
		api.GET("/redis/test", func(c *gin.Context) {
			if db.Redis == nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"status":  "error",
					"message": "Redis client not initialized",
				})
				return
			}

			// 测试 Redis 连接
			ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
			defer cancel()

			// 执行 PING 命令
			err := db.Redis.Ping(ctx)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"status":  "error",
					"message": "Redis ping failed",
					"error":   err.Error(),
				})
				return
			}

			// 测试 SET/GET 操作
			testKey := "test:connection"
			testValue := "redis_working"

			err = db.Redis.Set(ctx, testKey, testValue, 10*time.Second)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"status":  "error",
					"message": "Redis SET operation failed",
					"error":   err.Error(),
				})
				return
			}

			getValue, err := db.Redis.Get(ctx, testKey)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"status":  "error",
					"message": "Redis GET operation failed",
					"error":   err.Error(),
				})
				return
			}

			// 清理测试数据
			db.Redis.Del(ctx, testKey)

			c.JSON(http.StatusOK, gin.H{
				"status":     "ok",
				"message":    "Redis connection successful",
				"test_value": getValue,
			})
		})
	}

	// 启动服务器
	port := cfg.Server.Port
	if port == "" {
		port = ":8080"
	}
	if port[0] != ':' {
		port = ":" + port
	}

	log.Printf("Server starting on port %s", port)
	log.Printf("Environment: %s", cfg.Server.Environment)
	log.Printf("Health check: http://localhost%s/healthz", port)
	log.Printf("API docs will be available at: http://localhost%s/swagger/index.html", port)

	if err := r.Run(port); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}

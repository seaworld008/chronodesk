package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	// 从环境变量获取数据库连接
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "host=localhost user=postgres password=postgres dbname=gongdan port=5432 sslmode=disable"
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		fmt.Printf("Failed to connect database: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("🔧 填充测试数据...")

	// 1. 创建分类数据
	fmt.Println("📁 创建分类...")
	categories := []map[string]interface{}{
		{"name": "技术支持", "description": "技术问题和故障排除", "color": "#3b82f6"},
		{"name": "产品咨询", "description": "产品功能和使用咨询", "color": "#10b981"},
		{"name": "投诉建议", "description": "客户投诉和改进建议", "color": "#ef4444"},
		{"name": "账户问题", "description": "账户和权限相关问题", "color": "#f59e0b"},
		{"name": "计费问题", "description": "账单和付款相关", "color": "#8b5cf6"},
	}

	for _, cat := range categories {
		db.Exec("INSERT INTO categories (name, description, color, created_at, updated_at) VALUES (?, ?, ?, NOW(), NOW()) ON CONFLICT (name) DO NOTHING",
			cat["name"], cat["description"], cat["color"])
	}

	// 2. 获取分类 ID
	var categoryIDs []uint
	db.Raw("SELECT id FROM categories ORDER BY id").Scan(&categoryIDs)
	fmt.Printf("   找到 %d 个分类\n", len(categoryIDs))

	// 3. 更新工单数据
	fmt.Println("📝 更新工单测试数据...")

	// 客户名称列表
	customerNames := []string{
		"张三", "李四", "王五", "赵六", "钱七",
		"孙八", "周九", "吴十", "郑十一", "冯十二",
		"陈伟", "林小明", "黄大发", "刘建国", "杨芳",
	}

	// 标签列表
	tagOptions := []string{
		"紧急", "VIP客户", "需跟进", "待确认", "已升级",
		"技术难题", "产品缺陷", "新功能", "文档更新", "培训需求",
	}

	// 获取所有工单
	var ticketIDs []uint
	db.Raw("SELECT id FROM tickets").Scan(&ticketIDs)
	fmt.Printf("   找到 %d 条工单\n", len(ticketIDs))

	rand.Seed(time.Now().UnixNano())

	updated := 0
	for _, ticketID := range ticketIDs {
		// 随机选择数据
		customerName := customerNames[rand.Intn(len(customerNames))]

		// 随机 1-3 个标签
		numTags := rand.Intn(3) + 1
		selectedTags := make([]string, 0, numTags)
		for i := 0; i < numTags; i++ {
			tag := tagOptions[rand.Intn(len(tagOptions))]
			// 避免重复
			found := false
			for _, t := range selectedTags {
				if t == tag {
					found = true
					break
				}
			}
			if !found {
				selectedTags = append(selectedTags, tag)
			}
		}
		tagsJSON := "["
		for i, t := range selectedTags {
			if i > 0 {
				tagsJSON += ","
			}
			tagsJSON += fmt.Sprintf(`"%s"`, t)
		}
		tagsJSON += "]"

		// 随机分类
		var categoryID *uint
		if len(categoryIDs) > 0 && rand.Float32() > 0.2 { // 80% 有分类
			cid := categoryIDs[rand.Intn(len(categoryIDs))]
			categoryID = &cid
		}

		// 随机截止日期 (未来 1-14 天)
		dueDate := time.Now().AddDate(0, 0, rand.Intn(14)+1)

		// 更新工单
		result := db.Exec(`
			UPDATE tickets SET 
				customer_name = ?,
				tags = ?::jsonb,
				category_id = ?,
				due_date = ?,
				updated_at = NOW()
			WHERE id = ?
		`, customerName, tagsJSON, categoryID, dueDate, ticketID)

		if result.Error == nil && result.RowsAffected > 0 {
			updated++
		}
	}

	fmt.Printf("✅ 更新了 %d 条工单的测试数据\n", updated)
	fmt.Println("🎉 测试数据填充完成!")
}

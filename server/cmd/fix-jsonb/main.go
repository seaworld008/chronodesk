// JSONB 数据修复工具
// 用于修复在 JSONB 迁移后，数据库中存在的无效 JSON 数据
// 将空字符串或无效的 JSON 转换为有效的 JSON 格式

package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	dsn     string
	verbose bool
	dryRun  bool
)

func init() {
	// 尝试加载 .env 文件
	godotenv.Load()

	// 默认使用环境变量中的数据库连接
	defaultDSN := os.Getenv("DATABASE_URL")
	if defaultDSN == "" {
		defaultDSN = "host=localhost user=postgres password=postgres dbname=gongdan port=5432 sslmode=disable"
	}

	flag.StringVar(&dsn, "dsn", defaultDSN, "数据库连接字符串")
	flag.BoolVar(&verbose, "verbose", false, "显示详细输出")
	flag.BoolVar(&dryRun, "dry-run", false, "仅显示将要执行的操作，不实际修改数据")
}

func main() {
	flag.Parse()

	fmt.Println("=== JSONB 数据修复工具 ===")
	fmt.Println()

	if dryRun {
		fmt.Println("【预演模式】仅显示将要执行的操作")
		fmt.Println()
	}

	// 配置日志级别
	logLevel := logger.Silent
	if verbose {
		logLevel = logger.Info
	}

	// 连接数据库
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		log.Fatalf("无法连接数据库: %v", err)
	}

	fmt.Println("✓ 数据库连接成功")
	fmt.Println()

	// 执行修复
	if err := fixJSONBData(db); err != nil {
		log.Fatalf("修复失败: %v", err)
	}

	fmt.Println()
	fmt.Println("=== 修复完成 ===")
}

// fixJSONBData 修复 tickets 表中的 JSONB 字段
func fixJSONBData(db *gorm.DB) error {
	// 定义需要修复的字段及其默认值
	fields := []struct {
		column       string
		defaultValue string
		description  string
	}{
		{"tags", "[]", "标签列表"},
		{"attachments", "[]", "附件列表"},
		{"custom_fields", "{}", "自定义字段"},
	}

	for _, field := range fields {
		fmt.Printf("正在检查字段: %s (%s)\n", field.column, field.description)

		// 查询需要修复的记录数量
		var count int64
		query := fmt.Sprintf(`
			SELECT COUNT(*) FROM tickets 
			WHERE %s IS NULL 
			   OR %s::text = '' 
			   OR %s::text = '""'
			   OR (
			       %s::text NOT LIKE '[%%' 
			       AND %s::text NOT LIKE '{%%' 
			       AND %s::text != 'null'
			   )
		`, field.column, field.column, field.column, field.column, field.column, field.column)

		if err := db.Raw(query).Scan(&count).Error; err != nil {
			return fmt.Errorf("查询 %s 失败: %v", field.column, err)
		}

		if count == 0 {
			fmt.Printf("  → 无需修复（所有记录都是有效的 JSON）\n")
			continue
		}

		fmt.Printf("  → 发现 %d 条需要修复的记录\n", count)

		if dryRun {
			fmt.Printf("  → [预演] 将更新为: %s\n", field.defaultValue)
			continue
		}

		// 执行修复
		updateQuery := fmt.Sprintf(`
			UPDATE tickets 
			SET %s = '%s'::jsonb
			WHERE %s IS NULL 
			   OR %s::text = '' 
			   OR %s::text = '""'
			   OR (
			       %s::text NOT LIKE '[%%' 
			       AND %s::text NOT LIKE '{%%' 
			       AND %s::text != 'null'
			   )
		`, field.column, field.defaultValue, field.column, field.column, field.column, field.column, field.column, field.column)

		result := db.Exec(updateQuery)
		if result.Error != nil {
			return fmt.Errorf("更新 %s 失败: %v", field.column, result.Error)
		}

		fmt.Printf("  ✓ 成功修复 %d 条记录\n", result.RowsAffected)
	}

	return nil
}

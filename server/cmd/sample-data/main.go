package main

import (
	"flag"
	"log"
	"os"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gongdan-system/internal/database"
)

func main() {
	// 解析命令行参数
	var (
		cleanup = flag.Bool("cleanup", false, "清理示例数据而不是生成")
		force   = flag.Bool("force", false, "强制执行，即使在生产环境")
	)
	flag.Parse()

	// 加载环境变量
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: Could not load .env file: %v", err)
	}

	// 连接数据库
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL environment variable is not set")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// 检查环境
	environment := os.Getenv("ENVIRONMENT")
	if environment == "production" && !*force {
		log.Fatal("❌ 生产环境下不能生成示例数据。使用 --force 参数强制执行（不推荐）")
	}

	// 创建示例数据生成器
	generator := database.NewSampleDataGenerator(db)

	if *cleanup {
		log.Println("🗑️ 开始清理示例数据...")
		if err := generator.CleanupSampleData(); err != nil {
			log.Fatalf("清理示例数据失败: %v", err)
		}
		log.Println("✅ 示例数据清理完成")
	} else {
		log.Println("🚀 开始生成示例数据...")
		if err := generator.GenerateAllSampleData(); err != nil {
			log.Fatalf("生成示例数据失败: %v", err)
		}
		log.Println("✅ 示例数据生成完成")
		
		log.Println("\n📋 示例账号信息:")
		log.Println("技术支持: support@sample.com / DemoPass123!")
		log.Println("客户服务: service@sample.com / DemoPass123!")
		log.Println("项目经理: pm@sample.com / DemoPass123!")
		log.Println("普通用户1: user1@sample.com / DemoPass123!")
		log.Println("普通用户2: user2@sample.com / DemoPass123!")
		log.Println("普通用户3: user3@sample.com / DemoPass123!")
	}
}
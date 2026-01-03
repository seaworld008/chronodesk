package main

import (
	"fmt"
	"os/user"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	currentUser, _ := user.Current()
	username := currentUser.Username

	dsns := []string{
		"host=localhost user=postgres password=postgres dbname=gongdan port=5432 sslmode=disable",
		"host=localhost user=postgres password=password dbname=gongdan port=5432 sslmode=disable",
		"host=localhost user=postgres dbname=gongdan port=5432 sslmode=disable",                  // No password
		fmt.Sprintf("host=localhost user=%s dbname=gongdan port=5432 sslmode=disable", username), // Current user, no password
		fmt.Sprintf("host=localhost user=%s password=password dbname=gongdan port=5432 sslmode=disable", username),
		"host=127.0.0.1 user=postgres password=postgres dbname=gongdan port=5432 sslmode=disable",
	}

	fmt.Println("🕵️‍♀️ 开始探测数据库连接...")

	for _, dsn := range dsns {
		fmt.Printf("尝试连接: %s ... ", dsn)
		_, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err == nil {
			fmt.Println("✅ 成功!")
			fmt.Printf("\nSUCCESS_DSN: %s\n", dsn)
			return
		}
		fmt.Printf("❌ 失败: %v\n", err)
	}

	fmt.Println("😢 所有尝试都失败了")
}

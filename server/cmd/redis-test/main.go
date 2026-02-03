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

	"github.com/go-redis/redis/v8"
	"github.com/joho/godotenv"
)

func main() {
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

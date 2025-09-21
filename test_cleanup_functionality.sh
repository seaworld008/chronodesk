#!/bin/bash

# 测试登录日志自动清理功能脚本
# Test script for login history auto-cleanup functionality

set -e

echo "=== 开始测试登录日志自动清理功能 ==="
echo "=== Testing Login History Auto-Cleanup Functionality ==="
echo ""

# 测试配置
BASE_URL="http://localhost:8080"
AUTH_TOKEN="test-token"
HEADERS="Authorization: Bearer $AUTH_TOKEN"

# 颜色输出函数
print_success() {
    echo -e "\033[32m✅ $1\033[0m"
}

print_error() {
    echo -e "\033[31m❌ $1\033[0m"
}

print_info() {
    echo -e "\033[34mℹ️  $1\033[0m"
}

print_test() {
    echo -e "\033[33m🔍 $1\033[0m"
}

# 测试服务器是否运行
test_server_health() {
    print_test "1. 测试服务器健康状态"
    
    if curl -s "$BASE_URL/healthz" | grep -q "ok"; then
        print_success "服务器运行正常"
        return 0
    else
        print_error "服务器未运行或不健康"
        return 1
    fi
}

# 测试获取清理配置
test_get_cleanup_config() {
    print_test "2. 测试获取清理配置"
    
    response=$(curl -s -H "$HEADERS" "$BASE_URL/api/admin/system/cleanup/config")
    
    if echo "$response" | grep -q "success.*true"; then
        print_success "成功获取清理配置"
        echo "$response" | jq '.' 2>/dev/null || echo "$response"
        return 0
    else
        print_error "获取清理配置失败"
        echo "$response"
        return 1
    fi
}

# 测试更新清理配置
test_update_cleanup_config() {
    print_test "3. 测试更新清理配置"
    
    config_data='{
        "login_history_retention_days": 7,
        "cleanup_enabled": true,
        "cleanup_schedule": "0 3 * * *",
        "max_records_per_cleanup": 500
    }'
    
    response=$(curl -s -X PUT -H "$HEADERS" -H "Content-Type: application/json" \
        -d "$config_data" "$BASE_URL/api/admin/system/cleanup/config")
    
    if echo "$response" | grep -q "success.*true"; then
        print_success "成功更新清理配置"
        echo "$response" | jq '.' 2>/dev/null || echo "$response"
        return 0
    else
        print_error "更新清理配置失败"
        echo "$response"
        return 1
    fi
}

# 测试手动执行清理
test_manual_cleanup() {
    print_test "4. 测试手动执行清理"
    
    cleanup_data='{"task_type": "login_history"}'
    
    response=$(curl -s -X POST -H "$HEADERS" -H "Content-Type: application/json" \
        -d "$cleanup_data" "$BASE_URL/api/admin/system/cleanup/execute")
    
    if echo "$response" | grep -q "success.*true"; then
        print_success "成功触发手动清理"
        echo "$response" | jq '.' 2>/dev/null || echo "$response"
        return 0
    else
        print_error "手动清理触发失败"
        echo "$response"
        return 1
    fi
}

# 测试获取清理日志
test_get_cleanup_logs() {
    print_test "5. 测试获取清理日志"
    
    response=$(curl -s -H "$HEADERS" "$BASE_URL/api/admin/system/cleanup/logs?limit=5")
    
    if echo "$response" | grep -q "success.*true"; then
        print_success "成功获取清理日志"
        echo "$response" | jq '.' 2>/dev/null || echo "$response"
        return 0
    else
        print_error "获取清理日志失败"
        echo "$response"
        return 1
    fi
}

# 测试获取清理统计
test_get_cleanup_stats() {
    print_test "6. 测试获取清理统计"
    
    response=$(curl -s -H "$HEADERS" "$BASE_URL/api/admin/system/cleanup/stats")
    
    if echo "$response" | grep -q "success.*true"; then
        print_success "成功获取清理统计"
        echo "$response" | jq '.' 2>/dev/null || echo "$response"
        return 0
    else
        print_error "获取清理统计失败（可能需要创建cleanup_logs表）"
        echo "$response"
        return 1
    fi
}

# 测试系统配置管理
test_system_config_management() {
    print_test "7. 测试系统配置管理"
    
    # 创建测试配置
    config_data='{
        "key": "test_config",
        "value": "test_value",
        "description": "测试配置项",
        "category": "test",
        "group": "testing"
    }'
    
    response=$(curl -s -X POST -H "$HEADERS" -H "Content-Type: application/json" \
        -d "$config_data" "$BASE_URL/api/admin/system/configs")
    
    if echo "$response" | grep -q "success.*true"; then
        print_success "成功创建系统配置"
        
        # 获取配置列表
        list_response=$(curl -s -H "$HEADERS" "$BASE_URL/api/admin/system/configs?category=test")
        if echo "$list_response" | grep -q "test_config"; then
            print_success "成功获取系统配置列表"
        fi
        
        return 0
    else
        print_error "创建系统配置失败"
        echo "$response"
        return 1
    fi
}

# 主测试函数
run_tests() {
    echo ""
    print_info "开始执行测试套件..."
    echo ""
    
    local failed_tests=0
    local total_tests=7
    
    # 执行各项测试
    test_server_health || ((failed_tests++))
    echo ""
    
    test_get_cleanup_config || ((failed_tests++))
    echo ""
    
    test_update_cleanup_config || ((failed_tests++))
    echo ""
    
    test_manual_cleanup || ((failed_tests++))
    echo ""
    
    test_get_cleanup_logs || ((failed_tests++))
    echo ""
    
    test_get_cleanup_stats || ((failed_tests++))
    echo ""
    
    test_system_config_management || ((failed_tests++))
    echo ""
    
    # 输出测试结果
    echo "========================================"
    echo "测试结果汇总 / Test Results Summary:"
    echo "========================================"
    
    local passed_tests=$((total_tests - failed_tests))
    echo "总测试数 / Total Tests: $total_tests"
    echo "通过测试 / Passed Tests: $passed_tests"
    echo "失败测试 / Failed Tests: $failed_tests"
    
    if [ $failed_tests -eq 0 ]; then
        print_success "所有测试通过！登录日志自动清理功能实现完整！"
        return 0
    else
        print_error "有 $failed_tests 个测试失败，需要进一步调试"
        return 1
    fi
}

# 功能特性总结
print_features() {
    echo ""
    echo "========================================"
    echo "已实现的功能特性 / Implemented Features:"
    echo "========================================"
    echo "✅ 1. 系统配置管理模型 (SystemConfig, CleanupConfig, CleanupLog)"
    echo "✅ 2. 灵活的数据清理服务 (CleanupService)"
    echo "✅ 3. 定时任务调度器 (Scheduler with cron-like functionality)"
    echo "✅ 4. 完整的REST API接口 (SystemHandler with 12 endpoints)"
    echo "✅ 5. 登录日志批量清理功能"
    echo "✅ 6. 手动清理触发机制"
    echo "✅ 7. 清理任务监控和日志记录"
    echo "✅ 8. 清理统计信息查询"
    echo "✅ 9. 可配置的保留策略"
    echo "✅ 10. 数据库迁移和索引优化"
    echo ""
    echo "API端点 / API Endpoints:"
    echo "- GET    /api/admin/system/configs                - 获取系统配置列表"
    echo "- POST   /api/admin/system/configs               - 创建系统配置"
    echo "- PUT    /api/admin/system/configs/:key          - 更新系统配置"
    echo "- DELETE /api/admin/system/configs/:key          - 删除系统配置"
    echo "- GET    /api/admin/system/configs/:key          - 获取单个配置"
    echo "- GET    /api/admin/system/cleanup/config        - 获取清理配置"
    echo "- PUT    /api/admin/system/cleanup/config        - 更新清理配置"
    echo "- POST   /api/admin/system/cleanup/execute       - 手动执行清理"
    echo "- POST   /api/admin/system/cleanup/execute-all   - 执行所有清理任务"
    echo "- GET    /api/admin/system/cleanup/logs          - 获取清理日志"
    echo "- GET    /api/admin/system/cleanup/stats         - 获取清理统计"
    echo ""
}

# 主入口
main() {
    print_features
    
    # 检查是否安装了jq（用于JSON格式化）
    if ! command -v jq &> /dev/null; then
        print_info "提示：安装jq工具可获得更好的JSON输出格式"
    fi
    
    run_tests
    local exit_code=$?
    
    echo ""
    print_info "测试完成！"
    
    if [ $exit_code -eq 0 ]; then
        echo ""
        print_success "🎉 FE004 - 登录日志自动清理机制 实现完成！"
        echo ""
        echo "下一步建议："
        echo "1. 创建前端系统配置管理界面"
        echo "2. 完善错误处理和用户体验"
        echo "3. 添加更多清理任务类型支持"
        echo "4. 实现清理任务的暂停/恢复功能"
    fi
    
    exit $exit_code
}

# 执行主函数
main "$@"
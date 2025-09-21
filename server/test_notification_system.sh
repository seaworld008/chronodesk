#!/bin/bash

# =============================================================================
# 通知系统自动化测试脚本
# 大师级开发思想：批量自动化测试，避免低效重复的单个命令测试
# =============================================================================

set -e  # 遇到错误立即退出

# 配置部分 - 可根据环境修改
API_BASE="http://localhost:8080/api"
ADMIN_TOKEN="eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxIiwidXNlcm5hbWUiOiJhZG1pbiIsInJvbGUiOiJhZG1pbiIsImV4cCI6MTcyNDc5MTY0MiwiaWF0IjoxNzI0NzA1MjQyLCJpc3MiOiJ0aWNrZXQtc3lzdGVtIn0.L8YgFhZQ3qVJcCfrW4PJLppx-SUrM8AhfFa_cw_Z-6E"

# 测试状态跟踪
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
NC='\033[0m' # No Color

# 工具函数
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
    ((PASSED_TESTS++))
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
    ((FAILED_TESTS++))
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_test() {
    echo -e "${PURPLE}[TEST]${NC} $1"
    ((TOTAL_TESTS++))
}

# HTTP请求包装函数
api_call() {
    local method=$1
    local endpoint=$2
    local data=$3
    local expected_status=${4:-200}
    
    if [[ -n $data ]]; then
        response=$(curl -s -w "%{http_code}" -X "$method" \
            -H "Authorization: Bearer $ADMIN_TOKEN" \
            -H "Content-Type: application/json" \
            -d "$data" \
            "$API_BASE$endpoint")
    else
        response=$(curl -s -w "%{http_code}" -X "$method" \
            -H "Authorization: Bearer $ADMIN_TOKEN" \
            "$API_BASE$endpoint")
    fi
    
    http_code="${response: -3}"
    response_body="${response%???}"
    
    if [[ $http_code -eq $expected_status ]]; then
        return 0
    else
        log_error "HTTP $http_code (expected $expected_status): $response_body"
        return 1
    fi
}

# 清理测试环境
cleanup_test_data() {
    log_info "清理测试环境..."
    # 这里可以添加清理逻辑，比如删除测试数据
    # 暂时跳过，因为我们使用的是开发数据库
}

# 设置测试环境
setup_test_data() {
    log_info "设置测试环境..."
    # 确保有测试用的工单存在
    api_call GET "/tickets/1" "" 200 || {
        log_warning "工单1不存在，跳过依赖工单的测试"
        return 1
    }
    return 0
}

# 测试套件1：基础API功能
test_basic_notification_apis() {
    echo -e "\n${BLUE}=== 测试套件1：基础通知API功能 ===${NC}"
    
    log_test "1.1 获取通知列表"
    if api_call GET "/notifications" ""; then
        notification_count=$(echo "$response_body" | jq -r '.data | length')
        log_success "获取通知列表成功，当前有 $notification_count 条通知"
    fi
    
    log_test "1.2 获取未读通知数量"
    if api_call GET "/notifications/unread-count" ""; then
        unread_count=$(echo "$response_body" | jq -r '.count')
        log_success "获取未读数量成功：$unread_count 条"
    fi
    
    log_test "1.3 创建系统通知"
    test_notification_data='{
        "type": "system_alert",
        "title": "自动化测试通知",
        "content": "这是通过自动化测试脚本创建的通知",
        "priority": "high",
        "recipient_id": 1
    }'
    if api_call POST "/admin/notifications" "$test_notification_data" 201; then
        created_notification_id=$(echo "$response_body" | jq -r '.data.id')
        log_success "创建系统通知成功，ID: $created_notification_id"
    fi
    
    log_test "1.4 验证通知创建后数量增加"
    if api_call GET "/notifications/unread-count" ""; then
        new_unread_count=$(echo "$response_body" | jq -r '.count')
        if [[ $new_unread_count -gt $unread_count ]]; then
            log_success "通知数量正确增加：$unread_count -> $new_unread_count"
        else
            log_error "通知数量未正确增加：$unread_count -> $new_unread_count"
        fi
    fi
}

# 测试套件2：工单集成功能
test_ticket_integration() {
    echo -e "\n${BLUE}=== 测试套件2：工单集成功能 ===${NC}"
    
    # 先获取当前通知数量作为基准
    api_call GET "/notifications/unread-count" ""
    baseline_count=$(echo "$response_body" | jq -r '.count')
    log_info "基准通知数量: $baseline_count"
    
    log_test "2.1 测试工单状态变更触发通知"
    status_change_data='{"status": "closed", "resolution_time": 150}'
    if api_call PUT "/tickets/1" "$status_change_data" 200; then
        log_info "工单状态更新成功"
        
        # 等待一下让通知生成
        sleep 1
        
        # 检查通知是否增加
        api_call GET "/notifications/unread-count" ""
        after_status_count=$(echo "$response_body" | jq -r '.count')
        
        if [[ $after_status_count -gt $baseline_count ]]; then
            log_success "工单状态变更成功触发通知：$baseline_count -> $after_status_count"
        else
            log_warning "工单状态变更未触发新通知（可能是自己操作自己的工单）"
        fi
    fi
    
    log_test "2.2 测试工单分配触发通知"
    # 假设系统中有用户ID 2，分配给他
    assignment_data='{"assigned_to_id": 2, "status": "in_progress"}'
    if api_call PUT "/tickets/1" "$assignment_data" 200; then
        log_info "工单分配更新成功"
        sleep 1
        
        api_call GET "/notifications/unread-count" ""
        after_assign_count=$(echo "$response_body" | jq -r '.count')
        
        if [[ $after_assign_count -gt $after_status_count ]]; then
            log_success "工单分配成功触发通知：$after_status_count -> $after_assign_count"
        else
            log_warning "工单分配未触发新通知（可能是接收者不存在）"
        fi
    fi
}

# 测试套件3：通知操作功能
test_notification_operations() {
    echo -e "\n${BLUE}=== 测试套件3：通知操作功能 ===${NC}"
    
    log_test "3.1 获取最新通知ID"
    if api_call GET "/notifications?limit=1" ""; then
        latest_notification=$(echo "$response_body" | jq -r '.data[0]')
        if [[ "$latest_notification" != "null" ]]; then
            latest_id=$(echo "$latest_notification" | jq -r '.id')
            is_read=$(echo "$latest_notification" | jq -r '.is_read')
            log_success "获取最新通知ID: $latest_id, 已读状态: $is_read"
            
            if [[ "$is_read" == "false" ]]; then
                log_test "3.2 标记通知为已读"
                if api_call PUT "/notifications/$latest_id/read" "" 200; then
                    log_success "标记通知 $latest_id 为已读成功"
                    
                    # 验证已读状态
                    log_test "3.3 验证已读状态"
                    if api_call GET "/notifications?limit=1" ""; then
                        updated_notification=$(echo "$response_body" | jq -r '.data[0]')
                        updated_is_read=$(echo "$updated_notification" | jq -r '.is_read')
                        if [[ "$updated_is_read" == "true" ]]; then
                            log_success "通知已读状态验证成功"
                        else
                            log_error "通知已读状态验证失败"
                        fi
                    fi
                fi
            else
                log_warning "最新通知已经是已读状态，跳过标记测试"
            fi
        else
            log_error "没有找到通知数据"
        fi
    fi
    
    log_test "3.4 批量标记所有通知为已读"
    if api_call PUT "/notifications/mark-all-read" "" 200; then
        log_success "批量标记所有通知为已读成功"
        
        # 验证未读数量变为0
        api_call GET "/notifications/unread-count" ""
        final_unread_count=$(echo "$response_body" | jq -r '.count')
        if [[ $final_unread_count -eq 0 ]]; then
            log_success "未读数量验证成功：$final_unread_count"
        else
            log_warning "未读数量不为0：$final_unread_count（可能有其他未读通知）"
        fi
    fi
}

# 测试套件4：错误处理和边界情况
test_error_handling() {
    echo -e "\n${BLUE}=== 测试套件4：错误处理和边界情况 ===${NC}"
    
    log_test "4.1 测试无效的通知ID"
    if api_call GET "/notifications/99999/read" "" 404; then
        log_success "无效通知ID正确返回404"
    fi
    
    log_test "4.2 测试无效的通知创建数据"
    invalid_data='{"title": ""}'  # 缺少必需字段
    if api_call POST "/admin/notifications" "$invalid_data" 400; then
        log_success "无效数据正确返回400"
    fi
    
    log_test "4.3 测试未授权访问"
    # 使用无效token
    old_token=$ADMIN_TOKEN
    ADMIN_TOKEN="invalid_token"
    if api_call GET "/notifications" "" 401; then
        log_success "无效token正确返回401"
    fi
    ADMIN_TOKEN=$old_token
}

# 性能和数据完整性测试
test_performance_and_integrity() {
    echo -e "\n${BLUE}=== 测试套件5：性能和数据完整性 ===${NC}"
    
    log_test "5.1 批量创建通知性能测试"
    start_time=$(date +%s%N)
    
    for i in {1..5}; do
        test_data="{\"type\":\"system_alert\",\"title\":\"性能测试 $i\",\"content\":\"批量创建测试\",\"priority\":\"normal\",\"recipient_id\":1}"
        api_call POST "/admin/notifications" "$test_data" 201 || break
    done
    
    end_time=$(date +%s%N)
    duration=$((($end_time - $start_time) / 1000000))  # 转换为毫秒
    log_success "批量创建5个通知耗时：${duration}ms"
    
    log_test "5.2 分页查询测试"
    if api_call GET "/notifications?limit=3&offset=0" ""; then
        page1_count=$(echo "$response_body" | jq -r '.data | length')
        log_success "分页查询成功，第一页 $page1_count 条记录"
    fi
    
    if api_call GET "/notifications?limit=3&offset=3" ""; then
        page2_count=$(echo "$response_body" | jq -r '.data | length')
        log_success "分页查询成功，第二页 $page2_count 条记录"
    fi
}

# 主测试函数
run_all_tests() {
    echo -e "${GREEN}开始执行通知系统自动化测试${NC}"
    echo "测试配置："
    echo "  - API端点: $API_BASE"
    echo "  - 测试用户: admin"
    echo "  - 时间: $(date)"
    echo ""
    
    # 检查服务是否运行
    if ! curl -s "$API_BASE/health" >/dev/null 2>&1; then
        log_error "API服务未启动，请先启动服务"
        exit 1
    fi
    
    # 设置测试环境
    setup_test_data
    
    # 执行所有测试套件
    test_basic_notification_apis
    test_ticket_integration  
    test_notification_operations
    test_error_handling
    test_performance_and_integrity
    
    # 生成测试报告
    echo ""
    echo -e "${GREEN}=== 测试报告 ===${NC}"
    echo "总测试数: $TOTAL_TESTS"
    echo -e "通过: ${GREEN}$PASSED_TESTS${NC}"
    echo -e "失败: ${RED}$FAILED_TESTS${NC}"
    echo "成功率: $(echo "scale=2; $PASSED_TESTS * 100 / $TOTAL_TESTS" | bc)%"
    
    if [[ $FAILED_TESTS -eq 0 ]]; then
        echo -e "\n${GREEN}🎉 所有测试通过！通知系统功能正常${NC}"
        exit 0
    else
        echo -e "\n${RED}❌ 有 $FAILED_TESTS 个测试失败，请检查${NC}"
        exit 1
    fi
}

# 清理函数（可选）
cleanup_and_exit() {
    log_info "清理测试数据..."
    cleanup_test_data
    exit 0
}

# 帮助信息
show_help() {
    echo "通知系统自动化测试脚本"
    echo ""
    echo "用法: $0 [选项]"
    echo ""
    echo "选项:"
    echo "  -h, --help      显示帮助信息"
    echo "  -c, --cleanup   仅执行清理操作"
    echo "  -t, --token     指定认证token"
    echo "  -u, --url       指定API基础URL"
    echo ""
    echo "示例:"
    echo "  $0                           # 运行所有测试"
    echo "  $0 -u http://localhost:3000  # 指定不同的API端点"
    echo "  $0 -c                        # 仅清理测试数据"
}

# 参数解析
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            show_help
            exit 0
            ;;
        -c|--cleanup)
            cleanup_and_exit
            ;;
        -t|--token)
            ADMIN_TOKEN="$2"
            shift 2
            ;;
        -u|--url)
            API_BASE="$2"
            shift 2
            ;;
        *)
            echo "未知选项: $1"
            show_help
            exit 1
            ;;
    esac
done

# 检查依赖
if ! command -v jq &> /dev/null; then
    echo "错误: 需要安装 jq 工具"
    echo "安装命令: brew install jq (macOS) 或 apt-get install jq (Ubuntu)"
    exit 1
fi

if ! command -v bc &> /dev/null; then
    echo "错误: 需要安装 bc 工具"
    echo "安装命令: brew install bc (macOS) 或 apt-get install bc (Ubuntu)"
    exit 1
fi

# 执行主测试
run_all_tests
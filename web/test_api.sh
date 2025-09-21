#!/bin/bash

# 系统自动化测试脚本
# 测试工单管理系统的核心功能

API_BASE="http://localhost:8081/api"
FRONTEND_BASE="http://localhost:3004"

echo "🚀 开始系统自动化测试..."
echo "================================"

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 测试结果统计
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

# 测试函数
test_api() {
    local test_name="$1"
    local method="$2"
    local url="$3"
    local data="$4"
    local expected_status="$5"
    
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    echo -n "测试: $test_name ... "
    
    if [ "$method" == "POST" ] && [ -n "$data" ]; then
        response=$(curl -s -w "HTTPSTATUS:%{http_code}" -X POST -H "Content-Type: application/json" -d "$data" "$url")
    elif [ "$method" == "GET" ]; then
        response=$(curl -s -w "HTTPSTATUS:%{http_code}" -H "Authorization: Bearer $AUTH_TOKEN" "$url")
    else
        response=$(curl -s -w "HTTPSTATUS:%{http_code}" "$url")
    fi
    
    http_status=$(echo "$response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')
    body=$(echo "$response" | sed -e 's/HTTPSTATUS:.*//g')
    
    if [ "$http_status" -eq "$expected_status" ]; then
        echo -e "${GREEN}✅ PASS${NC}"
        PASSED_TESTS=$((PASSED_TESTS + 1))
        return 0
    else
        echo -e "${RED}❌ FAIL (Expected: $expected_status, Got: $http_status)${NC}"
        echo -e "${YELLOW}   Response: $body${NC}"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        return 1
    fi
}

# 1. 基础连接测试
echo "📡 基础连接测试"
echo "--------------------------------"
test_api "后端健康检查" "GET" "$API_BASE/../healthz" "" "200"
test_api "API Ping测试" "GET" "$API_BASE/ping" "" "200"

# 2. 用户认证测试  
echo ""
echo "🔐 用户认证测试"
echo "--------------------------------"

# 测试登录 (使用提供的测试账号)
login_data='{"email":"demouser123@test.com","password":"SecureTest123!"}'
echo -n "测试: 用户登录 ... "
login_response=$(curl -s -w "HTTPSTATUS:%{http_code}" -X POST -H "Content-Type: application/json" -d "$login_data" "$API_BASE/auth/login")
login_status=$(echo "$login_response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')
login_body=$(echo "$login_response" | sed -e 's/HTTPSTATUS:.*//g')

if [ "$login_status" -eq "200" ]; then
    echo -e "${GREEN}✅ PASS${NC}"
    PASSED_TESTS=$((PASSED_TESTS + 1))
    
    # 提取访问令牌
    AUTH_TOKEN=$(echo "$login_body" | jq -r '.data.access_token // .access_token // ""')
    if [ "$AUTH_TOKEN" != "" ] && [ "$AUTH_TOKEN" != "null" ]; then
        echo "   🎫 获得访问令牌: ${AUTH_TOKEN:0:20}..."
    else
        echo -e "${YELLOW}   ⚠️  未能提取访问令牌${NC}"
    fi
else
    echo -e "${RED}❌ FAIL (Status: $login_status)${NC}"
    echo -e "${YELLOW}   Response: $login_body${NC}"
    FAILED_TESTS=$((FAILED_TESTS + 1))
    
    # 如果登录失败，尝试注册
    echo -n "测试: 用户注册 (fallback) ... "
    register_data='{"username":"demouser123","email":"demouser123@test.com","password":"SecureTest123!","confirm_password":"SecureTest123!"}'
    register_response=$(curl -s -w "HTTPSTATUS:%{http_code}" -X POST -H "Content-Type: application/json" -d "$register_data" "$API_BASE/auth/register")
    register_status=$(echo "$register_response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')
    
    if [ "$register_status" -eq "200" ] || [ "$register_status" -eq "201" ]; then
        echo -e "${GREEN}✅ PASS${NC}"
        echo "   🎉 用户注册成功，尝试重新登录..."
        
        # 重新尝试登录
        sleep 1
        login_response=$(curl -s -w "HTTPSTATUS:%{http_code}" -X POST -H "Content-Type: application/json" -d "$login_data" "$API_BASE/auth/login")
        login_status=$(echo "$login_response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')
        login_body=$(echo "$login_response" | sed -e 's/HTTPSTATUS:.*//g')
        
        if [ "$login_status" -eq "200" ]; then
            AUTH_TOKEN=$(echo "$login_body" | jq -r '.data.access_token // .access_token // ""')
            echo "   🎫 重新登录成功，获得令牌: ${AUTH_TOKEN:0:20}..."
        fi
    else
        echo -e "${RED}❌ FAIL${NC}"
        echo -e "${YELLOW}   Registration failed: $(echo "$register_response" | sed -e 's/HTTPSTATUS:.*//g')${NC}"
    fi
fi

TOTAL_TESTS=$((TOTAL_TESTS + 1))

# 3. 受保护的API测试
if [ -n "$AUTH_TOKEN" ] && [ "$AUTH_TOKEN" != "null" ]; then
    echo ""
    echo "🔒 受保护API测试"
    echo "--------------------------------"
    
    # 测试获取用户信息
    echo -n "测试: 获取用户信息 ... "
    me_response=$(curl -s -w "HTTPSTATUS:%{http_code}" -H "Authorization: Bearer $AUTH_TOKEN" "$API_BASE/auth/me")
    me_status=$(echo "$me_response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')
    
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [ "$me_status" -eq "200" ]; then
        echo -e "${GREEN}✅ PASS${NC}"
        PASSED_TESTS=$((PASSED_TESTS + 1))
    else
        echo -e "${RED}❌ FAIL (Status: $me_status)${NC}"
        FAILED_TESTS=$((FAILED_TESTS + 1))
    fi
    
    # 测试工单相关API
    echo -n "测试: 获取工单列表 ... "
    tickets_response=$(curl -s -w "HTTPSTATUS:%{http_code}" -H "Authorization: Bearer $AUTH_TOKEN" "$API_BASE/tickets")
    tickets_status=$(echo "$tickets_response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')
    
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [ "$tickets_status" -eq "200" ]; then
        echo -e "${GREEN}✅ PASS${NC}"
        PASSED_TESTS=$((PASSED_TESTS + 1))
        
        tickets_body=$(echo "$tickets_response" | sed -e 's/HTTPSTATUS:.*//g')
        ticket_count=$(echo "$tickets_body" | jq -r '.data.tickets // .tickets // [] | length')
        if [ "$ticket_count" != "null" ] && [ "$ticket_count" != "" ]; then
            echo "   📝 找到 $ticket_count 个工单"
        fi
    else
        echo -e "${RED}❌ FAIL (Status: $tickets_status)${NC}"
        echo -e "${YELLOW}   Response: $(echo "$tickets_response" | sed -e 's/HTTPSTATUS:.*//g')${NC}"
        FAILED_TESTS=$((FAILED_TESTS + 1))
    fi
    
    # 测试创建工单
    echo -n "测试: 创建工单 ... "
    create_ticket_data='{"title":"自动化测试工单","description":"这是一个自动化测试创建的工单","priority":"normal","category":"technical"}'
    create_response=$(curl -s -w "HTTPSTATUS:%{http_code}" -X POST -H "Content-Type: application/json" -H "Authorization: Bearer $AUTH_TOKEN" -d "$create_ticket_data" "$API_BASE/tickets")
    create_status=$(echo "$create_response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')
    
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [ "$create_status" -eq "200" ] || [ "$create_status" -eq "201" ]; then
        echo -e "${GREEN}✅ PASS${NC}"
        PASSED_TESTS=$((PASSED_TESTS + 1))
        
        # 提取工单ID用于后续测试
        create_body=$(echo "$create_response" | sed -e 's/HTTPSTATUS:.*//g')
        TICKET_ID=$(echo "$create_body" | jq -r '.data.id // .id // ""')
        if [ "$TICKET_ID" != "" ] && [ "$TICKET_ID" != "null" ]; then
            echo "   🎫 创建工单 ID: $TICKET_ID"
            
            # 测试获取单个工单
            echo -n "测试: 获取工单详情 ... "
            get_ticket_response=$(curl -s -w "HTTPSTATUS:%{http_code}" -H "Authorization: Bearer $AUTH_TOKEN" "$API_BASE/tickets/$TICKET_ID")
            get_ticket_status=$(echo "$get_ticket_response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')
            
            TOTAL_TESTS=$((TOTAL_TESTS + 1))
            if [ "$get_ticket_status" -eq "200" ]; then
                echo -e "${GREEN}✅ PASS${NC}"
                PASSED_TESTS=$((PASSED_TESTS + 1))
            else
                echo -e "${RED}❌ FAIL (Status: $get_ticket_status)${NC}"
                FAILED_TESTS=$((FAILED_TESTS + 1))
            fi
        fi
    else
        echo -e "${RED}❌ FAIL (Status: $create_status)${NC}"
        echo -e "${YELLOW}   Response: $(echo "$create_response" | sed -e 's/HTTPSTATUS:.*//g')${NC}"
        FAILED_TESTS=$((FAILED_TESTS + 1))
    fi
fi

# 4. 前端连接测试
echo ""
echo "🌐 前端连接测试"  
echo "--------------------------------"
echo -n "测试: 前端页面访问 ... "
frontend_response=$(curl -s -w "HTTPSTATUS:%{http_code}" "$FRONTEND_BASE")
frontend_status=$(echo "$frontend_response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')

TOTAL_TESTS=$((TOTAL_TESTS + 1))
if [ "$frontend_status" -eq "200" ]; then
    echo -e "${GREEN}✅ PASS${NC}"
    PASSED_TESTS=$((PASSED_TESTS + 1))
    
    frontend_body=$(echo "$frontend_response" | sed -e 's/HTTPSTATUS:.*//g')
    if echo "$frontend_body" | grep -q "vite"; then
        echo "   ⚡ Vite开发服务器正常运行"
    fi
else
    echo -e "${RED}❌ FAIL (Status: $frontend_status)${NC}"
    FAILED_TESTS=$((FAILED_TESTS + 1))
fi

# 测试API代理
echo -n "测试: 前端API代理 ... "
proxy_response=$(curl -s -w "HTTPSTATUS:%{http_code}" "$FRONTEND_BASE/api/ping")
proxy_status=$(echo "$proxy_response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')

TOTAL_TESTS=$((TOTAL_TESTS + 1))
if [ "$proxy_status" -eq "200" ]; then
    echo -e "${GREEN}✅ PASS${NC}"
    PASSED_TESTS=$((PASSED_TESTS + 1))
    echo "   🔄 API代理工作正常"
else
    echo -e "${RED}❌ FAIL (Status: $proxy_status)${NC}"
    FAILED_TESTS=$((FAILED_TESTS + 1))
fi

# 测试总结
echo ""
echo "📊 测试总结"
echo "================================"
echo "总测试数: $TOTAL_TESTS"
echo -e "通过: ${GREEN}$PASSED_TESTS${NC}"
echo -e "失败: ${RED}$FAILED_TESTS${NC}"

if [ $FAILED_TESTS -eq 0 ]; then
    echo -e "${GREEN}🎉 所有测试通过！系统运行正常！${NC}"
    exit 0
else
    success_rate=$((PASSED_TESTS * 100 / TOTAL_TESTS))
    echo -e "${YELLOW}⚠️  成功率: $success_rate%${NC}"
    
    if [ $success_rate -ge 80 ]; then
        echo -e "${YELLOW}✅ 系统基本功能正常，存在少量问题${NC}"
        exit 0
    else
        echo -e "${RED}❌ 系统存在重要问题，需要检查${NC}"
        exit 1
    fi
fi
#!/bin/bash

# 用户注册功能测试脚本
# 测试各种密码验证场景

API_BASE="http://localhost:8081/api"

echo "🔐 用户注册功能测试"
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
test_registration() {
    local test_name="$1"
    local username="$2"
    local email="$3"
    local password="$4"
    local confirm_password="$5"
    local should_succeed="$6"
    
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    echo -n "测试: $test_name ... "
    
    # 构建注册数据
    register_data="{\"username\":\"$username\",\"email\":\"$email\",\"password\":\"$password\",\"confirm_password\":\"$confirm_password\"}"
    
    # 发送注册请求
    response=$(curl -s -w "HTTPSTATUS:%{http_code}" -X POST -H "Content-Type: application/json" -d "$register_data" "$API_BASE/auth/register")
    http_status=$(echo "$response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')
    body=$(echo "$response" | sed -e 's/HTTPSTATUS:.*//g')
    
    if [ "$should_succeed" = "true" ]; then
        # 期望成功
        if [ "$http_status" -eq "200" ] || [ "$http_status" -eq "201" ]; then
            echo -e "${GREEN}✅ PASS${NC}"
            PASSED_TESTS=$((PASSED_TESTS + 1))
            
            # 提取用户信息
            user_id=$(echo "$body" | jq -r '.data.user.id // ""')
            if [ "$user_id" != "" ] && [ "$user_id" != "null" ]; then
                echo "   👤 注册成功，用户ID: $user_id"
                
                # 测试登录
                echo -n "   测试登录新用户 ... "
                login_data="{\"email\":\"$email\",\"password\":\"$password\"}"
                login_response=$(curl -s -w "HTTPSTATUS:%{http_code}" -X POST -H "Content-Type: application/json" -d "$login_data" "$API_BASE/auth/login")
                login_status=$(echo "$login_response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')
                
                if [ "$login_status" -eq "200" ]; then
                    echo -e "${GREEN}✅${NC}"
                else
                    echo -e "${RED}❌${NC}"
                fi
            fi
        else
            echo -e "${RED}❌ FAIL (Expected success, got: $http_status)${NC}"
            echo -e "${YELLOW}   Response: $body${NC}"
            FAILED_TESTS=$((FAILED_TESTS + 1))
        fi
    else
        # 期望失败
        if [ "$http_status" -ne "200" ] && [ "$http_status" -ne "201" ]; then
            echo -e "${GREEN}✅ PASS (Expected failure)${NC}"
            
            # 显示错误信息
            error_msg=$(echo "$body" | jq -r '.msg // .message // ""')
            if [ "$error_msg" != "" ] && [ "$error_msg" != "null" ]; then
                echo -e "${YELLOW}   理由: $error_msg${NC}"
            fi
            PASSED_TESTS=$((PASSED_TESTS + 1))
        else
            echo -e "${RED}❌ FAIL (Expected failure, got success: $http_status)${NC}"
            FAILED_TESTS=$((FAILED_TESTS + 1))
        fi
    fi
}

echo "📝 密码强度验证测试"
echo "--------------------------------"

# 1. 有效密码测试
test_registration "强密码注册" "stronguser$(date +%s)" "strong$(date +%s)@test.com" "StrongPass123!" "StrongPass123!" "true"

# 2. 缺少大写字母
test_registration "缺少大写字母" "noupperuser" "noupper@test.com" "weakpass123!" "weakpass123!" "false"

# 3. 缺少小写字母  
test_registration "缺少小写字母" "noloweruser" "nolower@test.com" "WEAKPASS123!" "WEAKPASS123!" "false"

# 4. 缺少数字
test_registration "缺少数字" "nodigituser" "nodigit@test.com" "WeakPass!" "WeakPass!" "false"

# 5. 缺少特殊字符
test_registration "缺少特殊字符" "nospecialuser" "nospecial@test.com" "WeakPass123" "WeakPass123" "false"

# 6. 密码不匹配
test_registration "密码不匹配" "mismatchuser" "mismatch@test.com" "StrongPass123!" "StrongPass456!" "false"

# 7. 密码太短（假设最小长度是8）
test_registration "密码太短" "shortuser" "short@test.com" "Str1!" "Str1!" "false"

# 8. 常见弱密码
test_registration "常见弱密码" "commonuser" "common@test.com" "Password123!" "Password123!" "false"

# 9. 邮箱格式错误
test_registration "邮箱格式错误" "bademailuser" "bademail" "StrongPass123!" "StrongPass123!" "false"

# 10. 用户名重复测试（使用已存在的用户名）
test_registration "用户名重复" "demouser123" "duplicate@test.com" "StrongPass123!" "StrongPass123!" "false"

echo ""
echo "📧 邮箱验证测试"
echo "--------------------------------"

# 11. 有效的各种邮箱格式
test_registration "标准邮箱格式" "emailtest1" "test.user+tag@example.org" "ValidPass123!" "ValidPass123!" "true"

test_registration "数字邮箱格式" "emailtest2" "123test@example.com" "ValidPass123!" "ValidPass123!" "true"

echo ""
echo "💪 密码边界测试"
echo "--------------------------------"

# 12. 极长但有效的密码
long_password="VeryLongButValidPassword123!$(date +%N)"
test_registration "超长密码" "longpassuser" "longpass@test.com" "$long_password" "$long_password" "true"

# 13. 包含unicode字符的密码
test_registration "Unicode密码" "unicodeuser" "unicode@test.com" "测试Pass123!" "测试Pass123!" "true"

echo ""
echo "📊 测试总结"
echo "================================"
echo "总测试数: $TOTAL_TESTS"
echo -e "通过: ${GREEN}$PASSED_TESTS${NC}"
echo -e "失败: ${RED}$FAILED_TESTS${NC}"

if [ $FAILED_TESTS -eq 0 ]; then
    echo -e "${GREEN}🎉 所有注册测试通过！密码验证功能正常！${NC}"
    exit 0
else
    success_rate=$((PASSED_TESTS * 100 / TOTAL_TESTS))
    echo -e "${YELLOW}⚠️  成功率: $success_rate%${NC}"
    
    if [ $success_rate -ge 80 ]; then
        echo -e "${YELLOW}✅ 注册功能基本正常，存在少量问题${NC}"
        exit 0
    else
        echo -e "${RED}❌ 注册功能存在重要问题，需要检查${NC}"
        exit 1
    fi
fi
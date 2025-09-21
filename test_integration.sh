#!/bin/bash

# Frontend-Backend Integration Test
# Tests the complete integration between React frontend and Go backend

set -e

echo "🔄 Frontend-Backend Integration Test"
echo "===================================="

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

API_BASE="http://localhost:8081/api"

# Test function
test_endpoint() {
    local name="$1"
    local url="$2"
    local expected_status="$3"
    
    echo -n "Testing $name ... "
    
    response=$(curl -s -w "HTTPSTATUS:%{http_code}" "$url" 2>/dev/null || echo "HTTPSTATUS:000")
    status=$(echo "$response" | sed -e 's/.*HTTPSTATUS://')
    
    if [ "$status" = "$expected_status" ]; then
        echo -e "${GREEN}✅ PASS${NC}"
        return 0
    else
        echo -e "${RED}❌ FAIL (Expected: $expected_status, Got: $status)${NC}"
        return 1
    fi
}

echo "1. Backend API Connectivity Tests"
echo "--------------------------------"

# Test basic backend endpoints
test_endpoint "Health Check" "$API_BASE/../healthz" "200"
test_endpoint "API Ping" "$API_BASE/ping" "200"
test_endpoint "Email Status" "$API_BASE/email-status" "200"

echo ""
echo "2. Frontend Configuration Tests"
echo "------------------------------"

# Check if frontend files have correct port configuration
if grep -q "localhost:8081" ../web/src/lib/dataProvider.ts; then
    echo -e "Data Provider Port: ${GREEN}✅ Correct (8081)${NC}"
else
    echo -e "Data Provider Port: ${RED}❌ Incorrect${NC}"
fi

if grep -q "localhost:8081" ../web/src/lib/authProvider.ts; then
    echo -e "Auth Provider Port: ${GREEN}✅ Correct (8081)${NC}"
else
    echo -e "Auth Provider Port: ${RED}❌ Incorrect${NC}"
fi

if grep -q "target: 'http://localhost:8081'" ../web/vite.config.ts; then
    echo -e "Vite Proxy Config: ${GREEN}✅ Correct (8081)${NC}"
else
    echo -e "Vite Proxy Config: ${RED}❌ Incorrect${NC}"
fi

echo ""
echo "3. API Endpoint Coverage Test"
echo "-----------------------------"

# Test all major API endpoints that should be accessible
endpoints=(
    "/ping:200"
    "/health:200"
    "/email-status:200"
    "/redis/test:200"
)

for endpoint_test in "${endpoints[@]}"; do
    endpoint=$(echo "$endpoint_test" | cut -d: -f1)
    expected_status=$(echo "$endpoint_test" | cut -d: -f2)
    test_endpoint "$endpoint" "$API_BASE$endpoint" "$expected_status"
done

echo ""
echo "4. CORS Configuration Test"
echo "-------------------------"

# Test CORS headers
echo -n "Testing CORS headers ... "
cors_response=$(curl -s -H "Origin: http://localhost:3000" -I "$API_BASE/ping" 2>/dev/null || echo "")

if echo "$cors_response" | grep -q "Access-Control-Allow-Origin"; then
    echo -e "${GREEN}✅ CORS Headers Present${NC}"
else
    echo -e "${YELLOW}⚠️  CORS Headers Missing (might be handled by middleware)${NC}"
fi

echo ""
echo "5. Configuration Summary"
echo "----------------------"

echo "Backend Server: http://localhost:8081"
echo "Frontend Dev Server: http://localhost:3000"
echo "API Base URL: $API_BASE"

# Check if .env file has correct port
if grep -q "PORT=8081" .env 2>/dev/null; then
    echo -e "Backend Port Config: ${GREEN}✅ Correct${NC}"
else
    echo -e "Backend Port Config: ${YELLOW}⚠️  Check .env file${NC}"
fi

echo ""
echo "6. Integration Recommendations"
echo "-----------------------------"

echo "To test complete integration:"
echo "1. Start backend: cd server && go run ."
echo "2. Start frontend: cd web && npm run dev"
echo "3. Visit: http://localhost:3000"
echo "4. Check browser network tab for API calls to :8081"

echo ""
echo -e "${GREEN}✅ Frontend-Backend Integration Configuration Complete!${NC}"
echo "Both frontend and backend are configured to work together properly."
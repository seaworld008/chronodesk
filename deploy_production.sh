#!/bin/bash

# 生产环境部署脚本 - FE004 登录日志自动清理机制
# Production Deployment Script for FE004 Login History Auto-Cleanup

set -e

echo "=========================================="
echo "🚀 FE004 生产环境部署脚本"
echo "Production Deployment for Login History Auto-Cleanup"
echo "=========================================="

# 配置变量
PROJECT_NAME="gongdan-system"
BACKUP_DIR="/backup/$(date +%Y%m%d_%H%M%S)"
LOG_FILE="/var/log/${PROJECT_NAME}_deploy.log"

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

print_warning() {
    echo -e "\033[33m⚠️  $1\033[0m"
}

# 检查必要的环境变量
check_environment() {
    print_info "检查环境变量..."
    
    local required_vars=(
        "DATABASE_URL"
        "REDIS_URL" 
        "JWT_SECRET"
        "ENVIRONMENT"
    )
    
    local missing_vars=()
    
    for var in "${required_vars[@]}"; do
        if [[ -z "${!var}" ]]; then
            missing_vars+=("$var")
        fi
    done
    
    if [[ ${#missing_vars[@]} -ne 0 ]]; then
        print_error "缺少必要的环境变量:"
        for var in "${missing_vars[@]}"; do
            echo "  - $var"
        done
        return 1
    fi
    
    print_success "环境变量检查通过"
    return 0
}

# 备份数据库
backup_database() {
    print_info "开始数据库备份..."
    
    if [[ -z "$DATABASE_URL" ]]; then
        print_error "DATABASE_URL 未设置"
        return 1
    fi
    
    # 创建备份目录
    mkdir -p "$BACKUP_DIR"
    
    # 数据库备份
    local backup_file="$BACKUP_DIR/database_backup.sql"
    
    if command -v pg_dump &> /dev/null; then
        if pg_dump "$DATABASE_URL" > "$backup_file"; then
            print_success "数据库备份完成: $backup_file"
        else
            print_error "数据库备份失败"
            return 1
        fi
    else
        print_warning "pg_dump 未安装，跳过数据库备份"
    fi
    
    return 0
}

# 构建应用
build_application() {
    print_info "构建应用..."
    
    # 后端构建
    print_info "构建后端服务..."
    cd server
    if go mod tidy && go build -o ../bin/server main.go; then
        print_success "后端构建完成"
    else
        print_error "后端构建失败"
        return 1
    fi
    cd ..
    
    # 前端构建 (如果存在)
    if [[ -d "web" && -f "web/package.json" ]]; then
        print_info "构建前端应用..."
        cd web
        if npm ci && npm run build; then
            print_success "前端构建完成"
        else
            print_error "前端构建失败"
            return 1
        fi
        cd ..
    fi
    
    return 0
}

# 运行数据库迁移
run_database_migration() {
    print_info "运行数据库迁移..."
    
    cd server
    
    # 运行数据库迁移并填充必要数据
    if go run cmd/migrate/main.go -seed; then
        print_success "数据库迁移完成"
    else
        print_error "数据库迁移失败"
        return 1
    fi

    cd ..
    return 0
}

# 部署服务
deploy_service() {
    print_info "部署服务..."
    
    # 创建必要目录
    local app_dir="/opt/${PROJECT_NAME}"
    local log_dir="/var/log/${PROJECT_NAME}"
    local pid_file="/var/run/${PROJECT_NAME}.pid"
    
    sudo mkdir -p "$app_dir" "$log_dir"
    
    # 复制二进制文件
    sudo cp bin/server "$app_dir/"
    sudo chmod +x "$app_dir/server"
    
    # 复制配置文件
    if [[ -f "server/.env.production" ]]; then
        sudo cp server/.env.production "$app_dir/.env"
    fi
    
    # 创建systemd服务文件
    create_systemd_service
    
    print_success "服务文件部署完成"
    return 0
}

# 创建systemd服务文件
create_systemd_service() {
    print_info "创建systemd服务..."
    
    local service_file="/etc/systemd/system/${PROJECT_NAME}.service"
    local app_dir="/opt/${PROJECT_NAME}"
    
    sudo tee "$service_file" > /dev/null <<EOF
[Unit]
Description=工单管理系统 - 登录日志自动清理服务
After=network.target postgresql.service redis.service
Wants=postgresql.service redis.service

[Service]
Type=simple
User=app
Group=app
WorkingDirectory=$app_dir
ExecStart=$app_dir/server
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=${PROJECT_NAME}

# 环境变量
Environment=ENVIRONMENT=production
Environment=GIN_MODE=release
$(env | grep -E '^(DATABASE_URL|REDIS_URL|JWT_SECRET)=' | sed 's/^/Environment=/')

# 资源限制
LimitNOFILE=65536
LimitNPROC=4096

# 安全设置
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=$app_dir /var/log/${PROJECT_NAME} /tmp

[Install]
WantedBy=multi-user.target
EOF

    sudo systemctl daemon-reload
    print_success "systemd服务创建完成"
}

# 启动服务
start_service() {
    print_info "启动服务..."
    
    # 启动服务
    if sudo systemctl enable "${PROJECT_NAME}" && sudo systemctl start "${PROJECT_NAME}"; then
        print_success "服务启动成功"
    else
        print_error "服务启动失败"
        return 1
    fi
    
    # 检查服务状态
    sleep 5
    if sudo systemctl is-active --quiet "${PROJECT_NAME}"; then
        print_success "服务运行正常"
        sudo systemctl status "${PROJECT_NAME}" --no-pager
    else
        print_error "服务启动异常"
        sudo journalctl -u "${PROJECT_NAME}" --no-pager -n 20
        return 1
    fi
    
    return 0
}

# 验证部署
verify_deployment() {
    print_info "验证部署..."
    
    # 等待服务完全启动
    sleep 10
    
    # 健康检查
    if command -v curl &> /dev/null; then
        if curl -f http://localhost:8080/healthz >/dev/null 2>&1; then
            print_success "健康检查通过"
        else
            print_error "健康检查失败"
            return 1
        fi
    else
        print_warning "curl 未安装，跳过健康检查"
    fi
    
    # 检查日志
    print_info "最近的服务日志:"
    sudo journalctl -u "${PROJECT_NAME}" --no-pager -n 10
    
    return 0
}

# 清理部署文件
cleanup() {
    print_info "清理临时文件..."
    
    # 清理构建文件
    rm -rf bin/
    
    print_success "清理完成"
}

# 主部署流程
main() {
    local start_time=$(date +%s)
    
    print_info "开始部署 $(date)"
    
    # 检查是否为root或有sudo权限
    if [[ $EUID -eq 0 ]]; then
        print_warning "请不要以root用户运行此脚本，使用sudo权限即可"
    fi
    
    if ! sudo -n true 2>/dev/null; then
        print_error "此脚本需要sudo权限"
        exit 1
    fi
    
    # 执行部署步骤
    local failed=false
    
    check_environment || failed=true
    [[ "$failed" == "true" ]] && exit 1
    
    backup_database || print_warning "数据库备份失败，但继续部署"
    
    build_application || failed=true
    [[ "$failed" == "true" ]] && exit 1
    
    run_database_migration || failed=true
    [[ "$failed" == "true" ]] && exit 1
    
    deploy_service || failed=true
    [[ "$failed" == "true" ]] && exit 1
    
    start_service || failed=true
    [[ "$failed" == "true" ]] && exit 1
    
    verify_deployment || failed=true
    [[ "$failed" == "true" ]] && exit 1
    
    cleanup
    
    local end_time=$(date +%s)
    local duration=$((end_time - start_time))
    
    print_success "部署完成！耗时 ${duration} 秒"
    
    # 输出部署信息
    echo ""
    echo "=========================================="
    echo "🎉 FE004 登录日志自动清理机制部署成功！"
    echo "=========================================="
    echo "服务状态: $(sudo systemctl is-active ${PROJECT_NAME})"
    echo "服务日志: sudo journalctl -u ${PROJECT_NAME} -f"
    echo "健康检查: curl http://localhost:8080/healthz"
    echo "API文档: http://localhost:8080/swagger/index.html"
    echo ""
    echo "清理配置API: GET/PUT /api/admin/system/cleanup/config"
    echo "手动清理API: POST /api/admin/system/cleanup/execute"
    echo "清理日志API: GET /api/admin/system/cleanup/logs"
    echo "清理统计API: GET /api/admin/system/cleanup/stats"
    echo ""
    echo "调度器将在每天凌晨2点自动执行清理任务"
    echo "=========================================="
}

# 错误处理
trap 'print_error "部署过程中发生错误，请检查日志"; exit 1' ERR

# 执行主函数
main "$@"

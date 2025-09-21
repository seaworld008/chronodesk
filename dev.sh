#!/bin/bash

# 工单管理系统开发脚本
# 用法: ./dev.sh [start|stop|restart]

set -e

# 配置
BACKEND_PORT=8081
FRONTEND_PORT=3000
PID_DIR="./pids"
BACKEND_PID="$PID_DIR/backend.pid"
FRONTEND_PID="$PID_DIR/frontend.pid"

# 创建PID目录
mkdir -p "$PID_DIR"

# 清理端口
cleanup_ports() {
    local ports=(3000 3001 3002 3003 3004 3005 8080 8081)
    for port in "${ports[@]}"; do
        lsof -ti:$port 2>/dev/null | xargs kill -9 2>/dev/null || true
    done
}

# 停止服务
stop() {
    echo "🛑 停止服务..."
    
    # 停止后端
    if [ -f "$BACKEND_PID" ] && kill -0 $(cat "$BACKEND_PID") 2>/dev/null; then
        kill $(cat "$BACKEND_PID") && echo "✅ 后端已停止"
        rm -f "$BACKEND_PID"
    fi
    
    # 停止前端
    if [ -f "$FRONTEND_PID" ] && kill -0 $(cat "$FRONTEND_PID") 2>/dev/null; then
        kill $(cat "$FRONTEND_PID") && echo "✅ 前端已停止"
        rm -f "$FRONTEND_PID"
    fi
    
    # 清理端口
    cleanup_ports
    sleep 1
}

# 启动服务
start() {
    echo "🚀 启动服务..."
    
    # 启动后端
    if [ ! -d "server" ]; then
        echo "❌ server 目录不存在"
        exit 1
    fi
    
    cd server
    echo "  启动后端服务..."
    PORT=$BACKEND_PORT nohup make run >../backend.log 2>&1 &
    echo $! > "../$BACKEND_PID"
    cd ..
    
    # 等待后端启动
    echo "  等待后端启动..."
    sleep 5
    
    # 检查后端状态
    for i in {1..10}; do
        if curl -s http://localhost:$BACKEND_PORT/healthz >/dev/null 2>&1; then
            echo "✅ 后端启动成功 - http://localhost:$BACKEND_PORT"
            break
        elif [ $i -eq 10 ]; then
            echo "❌ 后端启动失败，检查日志: tail backend.log"
            exit 1
        fi
        sleep 1
    done
    
    # 启动前端
    if [ ! -d "web" ]; then
        echo "❌ web 目录不存在"
        exit 1
    fi
    
    cd web
    echo "  启动前端服务..."
    nohup npm run dev >../frontend.log 2>&1 &
    echo $! > "../$FRONTEND_PID"
    cd ..
    
    # 检测前端端口
    echo "  等待前端启动..."
    sleep 8
    for port in 3000 3001 3002 3003 3004 3005; do
        if curl -s http://localhost:$port >/dev/null 2>&1; then
            echo "✅ 前端启动成功 - http://localhost:$port"
            break
        fi
    done
    
    echo "🎉 系统启动完成!"
}

# 重启服务
restart() {
    echo "🔄 重启服务..."
    stop
    sleep 2
    start
}

# 显示状态
status() {
    echo "📊 服务状态:"
    
    # 检查后端
    if [ -f "$BACKEND_PID" ] && kill -0 $(cat "$BACKEND_PID") 2>/dev/null; then
        echo "✅ 后端: 运行中 (PID: $(cat "$BACKEND_PID"))"
    else
        echo "❌ 后端: 未运行"
    fi
    
    # 检查前端
    if [ -f "$FRONTEND_PID" ] && kill -0 $(cat "$FRONTEND_PID") 2>/dev/null; then
        echo "✅ 前端: 运行中 (PID: $(cat "$FRONTEND_PID"))"
    else
        echo "❌ 前端: 未运行"
    fi
}

# 主逻辑
case "${1:-start}" in
    start)
        cleanup_ports
        start
        ;;
    stop)
        stop
        ;;
    restart)
        restart
        ;;
    status)
        status
        ;;
    *)
        echo "用法: $0 [start|stop|restart|status]"
        echo ""
        echo "  start   - 启动服务 (默认)"
        echo "  stop    - 停止服务"
        echo "  restart - 重启服务"
        echo "  status  - 查看状态"
        exit 1
        ;;
esac
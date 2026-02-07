# ChronoDesk

ChronoDesk 是一个基于 Go + React 的工单管理系统，支持认证安全、工单全生命周期、自动化规则、通知中心和管理后台。

## 快速开始

### 1. 安装依赖
```bash
make install-deps
cp server/.env.example server/.env
```

### 2. 启动开发环境
```bash
./dev.sh start
```
- API: `http://localhost:8081`
- Web: `http://localhost:3000`

### 3. 常用命令
```bash
make server-dev      # 仅启动后端
make web-dev         # 仅启动前端
make build           # 构建后端与前端
make test-server     # Go 单测
make test-web        # 前端 lint
make dev             # docker-compose 一体化启动
```

## 文档入口
- 项目权威手册：`docs/PROJECT_MANUAL.md`
- 文档索引：`docs/README.md`
- 测试指南：`docs/testing_guide.md`
- 任务与规划：`docs/planning/`、`docs/plans/`

## 目录结构
```text
server/   Go API 与业务逻辑
web/      React Admin 管理端
docs/     项目文档中心
```

## 说明
- 根目录保留 `AGENTS.md` 和 `CLAUDE.md` 供 AI agent 控制流程使用。
- 其余业务文档统一收敛在 `docs/`。

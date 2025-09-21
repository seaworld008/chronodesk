# 后端自动化测试说明

本目录用于承载基于 `pytest` 的端到端 API 测试。骨架已搭建完成，后续阶段会逐步补充业务用例。

## 工程结构
```
server/
  tests/
    README.md         # 说明文档（当前文件）
    conftest.py       # pytest 全局配置 & 常用 fixtures
    utils/
      __init__.py
      api.py          # 轻量 HTTP 客户端封装（支持重试、登录）
```

现有的历史脚本（`server/test_*.py`）将逐步迁移/拆分至 `tests` 目录下的分模块文件，便于维护。

## 运行准备
1. 确保 API 服务已启动：
   ```bash
   ./dev.sh start
   ```
2. 数据库应已执行最新迁移，且拥有可用于测试的管理员账号。
   - 默认使用 `.env` 中的账号（`manager@tickets.com` / `SecureTicket2025!@#$`）。
   - 如需自定义，可通过环境变量覆盖：
     ```bash
     export TEST_ADMIN_EMAIL="admin@example.com"
     export TEST_ADMIN_PASSWORD="Password123!"
     ```

## 快速运行
```bash
cd server
pytest tests -v
```
- `--api-base-url`：可覆盖默认的 `http://localhost:8081/api`
- `TEST_API_BASE_URL`：等效环境变量
- 当健康检查无法连接时，会自动 `skip` 整个测试集合，避免误判

## 后续规划
- **阶段 1**：补充工单生命周期用例，统一落地在 `tests/tickets/test_lifecycle.py`
- **阶段 2**：补充认证（登录/记住设备/刷新）用例
- **阶段 3**：自动化规则与系统设置等模块
- 将在 `docs/test_plan.md` 中同步进度与里程碑

欢迎在此基础上扩展或提交测试用例。

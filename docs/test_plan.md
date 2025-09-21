# 全量自动化测试实施计划

## 1. 目标与范围
- **目标**：为工单系统关键模块建立可重复、自动化的冒烟及回归测试，覆盖核心业务链路并提供一键执行脚本。
- **范围优先级**：
  1. 工单生命周期（创建、编辑、状态流转、评论、通知触发）
  2. 认证流程（注册、登录、OTP、记住设备、刷新令牌、注销）
  3. 自动化规则与日志（规则 CRUD、触发验证、日志查询）
  4. 系统配置（读取、更新、缓存刷新及策略生效）

## 2. 环境与依赖
- 数据库：使用 `server/.env` 中配置的 Postgres。每轮测试需确保迁移已执行。
- Redis：沿用现有 Upstash 配置，用于通知/自动化相关测试。
- 后端启动脚本：`./dev.sh`（或在 CI 中使用独立命令）。
- 测试执行：
  - Go 层：`cd server && go test ./...`
  - Python 层：`pytest`（将在 `server/tests/` 中统一组织）
  - 可选前端验证：后续评估是否引入 E2E（如 Playwright）。

## 3. 工作分阶段
| 阶段 | 模块 | 关键任务 | 产出 | 状态 |
| ---- | ---- | -------- | ---- | ---- |
| 0 | 基础设施 | 1. 梳理现有 Python 测试脚本<br>2. 统一 pytest 目录结构与 fixtures<br>3. 准备数据库/缓存种子脚本，保证测试可重复 | `server/tests/conftest.py`、`server/tests/utils/api.py`、说明文档等基础脚本 | ✅ Completed |
| 1 | 工单主流程 | 1. 编写 API 级测试：创建/更新/状态变更/评论<br>2. 验证通知发送（邮件/站内消息记录）<br>3. 建立一键运行命令（如 `make test-tickets`） | `server/tests/tickets/test_lifecycle.py` & 执行指南 | ✅ Completed |
| 2 | 认证流程 | 1. 覆盖注册/登录成功与失败场景<br>2. OTP & Trusted Device 记住设备/注销场景<br>3. Refresh Token & Logout 测试 | `tests/auth/test_auth_flows.py`（含 OTP 启用、记住设备撤销、失败场景） | ✅ Completed |
| 3 | 自动化规则+日志 | 1. 构造规则 CRUD 与触发用例<br>2. 验证日志记录查询接口<br>3. 结合模板/SLA/快速回复等支撑能力 | `tests/automation/test_rules.py`、`test_sla.py`、`test_templates.py`、`test_quick_replies.py` | ✅ Completed |
| 4 | 系统设置 | 1. API 测试配置读取/更新/批量操作<br>2. 缓存刷新与策略生效验证（如安全策略）<br>3. 集成 tests 到统一入口 | `tests/system/test_configs.py`、`test_cleanup.py` | ✅ Completed |
| 5 | 集成与 CI | 1. 整合 Go + pytest + 前端构建为单一命令<br>2. 生成执行报告（文本/HTML）<br>3. 在 README/文档中加入运行指南 | `make smoke`（auth+automation+system） | ◑ In Progress |

> 状态符号说明：☐ Planned ｜ ◑ In Progress ｜ ✅ Completed。

## 4. 里程碑
| 里程碑 | 内容 | 目标时间 | 状态 |
| ------ | ---- | -------- | ---- |
| M1 | 完成阶段 0 + 阶段 1，实现工单自动化测试 | T+3 日 | ☐ |
| M2 | 完成阶段 2（认证流程） | T+5 日 | ☐ |
| M3 | 完成阶段 3 + 4，并整合统一执行命令 | T+8 日 | ☐ |
| M4 | CI/文档完善，测试全链打通 | T+10 日 | ☐ |

## 5. 风险与应对
- **数据污染风险**：测试前后需清理数据库或使用特定命名隔离；计划通过 fixtures + teardown 控制。
- **外部依赖（邮件/第三方服务）**：必要时使用 mock 或跳过真实发送。
- **执行耗时**：阶段性测试可拆分；最终整合时可在 CI 中并行处理。

## 6. 下一步行动
- 阶段 0 基础设施已完成：新增 `tests` 目录骨架、共享 API 客户端、README 指南。
- 阶段 1 已完成：`tests/tickets/test_lifecycle.py` 覆盖创建→更新→分配→状态流转→通知校验，pytest 通过。
- 阶段 2 完成：`tests/auth/test_auth_flows.py` 覆盖刷新令牌、OTP 启用、可信设备记住/撤销、备用码登录与禁用流程，已纳入 `make smoke`。
- 阶段 3 完成：自动化套件（`tests/automation/`）覆盖规则 CRUD/统计/日志、SLA 配置、模板与快速回复 CRUD/使用。
- 阶段 4 完成：系统配置与清理接口通过 `tests/system/` 套件验证（含批量更新、导出导入、缓存维护、清理配置）。
- 阶段 5 进行中：已新增 `make smoke` 聚合命令与 README 指南，下一步接入 CI 并整理执行报告产出。

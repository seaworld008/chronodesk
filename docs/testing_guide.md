# 测试与质量控制指南

本指南汇总 ChronoDesk 项目的常规测试流程，并给出推荐的执行顺序，帮助团队成员在提交代码前完成必要的质量门槛。所有命令均在项目根目录执行，除非特别说明。

## 1. 准备工作
- 确保 Go、Node.js、Python 环境已就绪，并安装依赖：
  - `make install-deps`
  - `pip install -r server/requirements-test.txt`
- 如需依赖数据库/Redis，可先运行 `./dev.sh start` 或 `docker-compose up -d`。

## 2. 后端测试
1. **快速单测**：`cd server && go test ./...`
2. **代码格式与静态检查**（变更较多时建议执行）：
   - `cd server && make fmt`
   - `cd server && make vet`

## 3. 前端检查
- Lint：`cd web && npm run lint`
- （可选）构建：`cd web && npm run build`

> 注意：当前 ESLint 仍存在若干历史问题，若未修改相关文件可在提交说明中注明。

## 4. Python / Pytest 冒烟套件
- 推荐命令：`cd server && make smoke`
  - 覆盖认证、自动化、系统配置等核心路径。
  - HTML 报告输出至 `server/reports/`，控制台同时打印摘要。
- 如需针对某模块，可执行如：`cd server && pytest tests/automation -v`

## 5. 集成与健康检查
- Shell 冒烟脚本：`./test_integration.sh`
- 邮件/通知专项脚本：`server/test_notification_system.sh`
- 运行服务后访问 `http://localhost:8081/healthz` 或 React Admin 仪表盘确认 UI 状态。

## 6. 提交前检查清单
- [ ] Go 单测通过
- [ ] 关键模块的 lint / 格式化处理
- [ ] 前端 Lint（若改动前端）
- [ ] Pytest 冒烟套件（如改动后端核心逻辑）
- [ ] 相关脚本执行结果记录在 PR 描述的 “Checks” 部分

如需扩展新的测试脚本，请在本文件中补充执行方式，保持团队共识。

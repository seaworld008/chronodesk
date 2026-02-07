# 文档中心（docs）

本目录是 ChronoDesk 的统一文档入口。阅读顺序按“先总览、再专题、后归档”组织，避免多份文档描述不一致。

## 1. 必读文档
- `PROJECT_MANUAL.md`：项目权威手册（功能、架构、API 地图、运行与测试标准）。
- `../README.md`：根目录快速上手入口（安装、启动、常用命令）。

## 2. 参考文档（Reference）
- `reference/API_DOCUMENTATION.md`：历史/扩展 API 说明（手工维护版本）。
- `reference/REQUIRED_API_ENDPOINTS.md`：功能需求阶段沉淀的接口清单。
- `../server/API_DOCUMENTATION.md`：后端侧 API 文档（与服务代码更接近）。

## 3. 规划与交接
- `planning/`：任务跟踪、阶段计划、里程碑文档。
- `plans/`：实现计划（按日期命名）。
- `handovers/`：会话交接文档。

## 4. 历史归档
- `archive/root-legacy/`：从根目录迁移的历史测试记录与初始化文档。
- `archive/agent-control/`：解释为何 `AGENTS.md`、`CLAUDE.md` 仍保留在根目录。

## 5. 文档维护规则
- 架构或接口变更时，先更新 `PROJECT_MANUAL.md`。
- 新增专题文档时，必须在本索引登记用途与位置。
- 不再活跃的文档迁移到 `archive/`，不要直接删除。

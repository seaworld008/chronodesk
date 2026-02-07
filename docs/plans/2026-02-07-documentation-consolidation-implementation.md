# Documentation Consolidation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将根目录分散文档合并为一套以 `docs/` 为中心的权威文档体系，并确保新成员/AI agent 可快速理解当前项目能力、架构与运行方式。

**Architecture:** 采用“单一权威手册 + 文档索引 + 参考文档分层归档”的信息架构。`docs/PROJECT_MANUAL.md` 作为唯一全景真相源，`docs/README.md` 负责导航，根目录 `README.md` 仅保留快速入口并指向 docs。

**Tech Stack:** Markdown, Git, shell consistency checks (`find`, `grep`, `git status`).

---

### Task 1: 盘点与信息架构定稿

**Files:**
- Read: `README.md`
- Read: `docs/project_overview.md`
- Read: `docs/project_summary.md`
- Read: `server/main.go`
- Read: `Makefile`

**Step 1: 核对代码事实源**
Run: `sed -n '1,320p' server/main.go`
Expected: 可确认 API 分组、功能模块、运行端口等事实。

**Step 2: 确定目标文档结构**
Run: `find . -maxdepth 2 -name '*.md' | sort`
Expected: 能明确根目录待迁移文档与 docs 现有文档。

### Task 2: 建立新文档骨架

**Files:**
- Create: `docs/README.md`
- Create: `docs/PROJECT_MANUAL.md`
- Modify: `README.md`

**Step 1: 编写 docs 索引**
实现 `docs/README.md`：定义权威文档、参考文档、归档规则与阅读顺序。

**Step 2: 编写项目权威手册**
实现 `docs/PROJECT_MANUAL.md`：覆盖架构、模块、API 地图、运行、测试、安全与运维。

**Step 3: 收敛根 README**
重写 `README.md`：保留快速上手与 docs 导航，避免与手册重复冲突。

### Task 3: 文档迁移与归档

**Files:**
- Create: `docs/reference/README.md`
- Create: `docs/archive/root-legacy/README.md`
- Create: `docs/archive/agent-control/README.md`
- Move: `API_DOCUMENTATION.md` -> `docs/reference/API_DOCUMENTATION.md`
- Move: `REQUIRED_API_ENDPOINTS.md` -> `docs/reference/REQUIRED_API_ENDPOINTS.md`
- Move: `test-backend-config.md` -> `docs/archive/root-legacy/test-backend-config.md`
- Move: `test-database-models.md` -> `docs/archive/root-legacy/test-database-models.md`
- Move: `test-init.md` -> `docs/archive/root-legacy/test-init.md`

**Step 1: 创建层级目录与说明**
为 `reference` 与 `archive` 建立 README，标注用途。

**Step 2: 迁移根目录文档**
执行文件迁移，确保根目录仅保留必要入口文档。

### Task 4: 一致性校验与引用修复

**Files:**
- Modify (if needed): `docs/project_overview.md`
- Modify (if needed): `docs/project_summary.md`
- Modify (if needed): `docs/testing_guide.md`

**Step 1: 扫描旧路径引用**
Run: `grep -R "API_DOCUMENTATION.md\|REQUIRED_API_ENDPOINTS.md\|test-backend-config.md\|test-database-models.md\|test-init.md" . --exclude-dir=.git --exclude-dir=node_modules`
Expected: 仅保留新路径引用，或在归档文档中注明“历史路径”。

**Step 2: 修复关键入口文档链接**
确保根 `README.md` 与 `docs/README.md` 链接可达，且描述不冲突。

### Task 5: 变更核对与交付

**Files:**
- Validate: all changed markdown files

**Step 1: 核查变更范围**
Run: `git status --short`
Expected: 仅包含本轮文档重构相关变更。

**Step 2: 输出交付摘要**
整理新增/迁移/重写清单、保留文件原因、后续可选动作（提交与 push）。

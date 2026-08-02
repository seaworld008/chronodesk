# React Router GHSA-qwww-vcr4-c8h2 处置决策

日期：2026-08-02

## 结论

ChronoDesk 使用的 `react-router` 与 `react-router-dom` 均为 `7.18.2`。该版本已包含
React Router 官方对 `GHSA-qwww-vcr4-c8h2` 的 v7 回移修复，不是仍在运行易受攻击的
版本。

GitHub 全局 Advisory 仍暂时把 `>=7.12.0, <8.3.0` 标为受影响范围，因此
Dependabot 与 `npm audit` 会继续把 `7.18.2` 报为高危。React Router 项目自己的
Advisory 已给出正确边界：

- v7：`>=7.12.0, <7.18.2` 受影响，`>=7.18.2` 已修复；
- v8：`>=8.0.0, <8.3.0` 受影响，`>=8.3.0` 已修复。

上游修复和元数据修正证据：

- React Router 回移修复：[remix-run/react-router#15353](https://github.com/remix-run/react-router/pull/15353)
- `7.18.2` 发布记录：[remix-run/react-router#15354](https://github.com/remix-run/react-router/pull/15354)
- 项目 Advisory：[GHSA-qwww-vcr4-c8h2](https://github.com/remix-run/react-router/security/advisories/GHSA-qwww-vcr4-c8h2)
- 等待合并的全局 Advisory 修正：[github/advisory-database#8868](https://github.com/github/advisory-database/pull/8868)

## 为什么不强制升级到 8.3.0

截至本决策日期，npm 上最新的 React Admin 仍为 `5.15.1`。其自身以及
`ra-core@5.15.0`、`ra-ui-materialui@5.15.1` 都只声明支持
`react-router`/`react-router-dom` `^6.28.1 || ^7.1.1`。

React Router v8 还移除了 `react-router-dom` 包并改为 ESM-only。ChronoDesk 与
React Admin 当前都直接使用 `react-router-dom`。用 npm `overrides` 把 v8 强塞到
现有依赖树会绕过兼容约束，并可能产生多个 Router Context；它不是可接受的安全修复。

React Admin 的 v8 Adapter 工作仍在
[marmelab/react-admin#11289](https://github.com/marmelab/react-admin/pull/11289)
中进行。待 React Admin 发布正式支持版本后，应整体升级 React Admin、Router Adapter
与应用导入路径，而不是在本次误报处置中制造私有兼容分叉。

## 仓库安全门

`web/scripts/audit-security.mjs` 执行以下 fail-closed 检查：

1. `react-router-dom` 必须解析到 `react-router@7.18.2`；
2. 已安装包的 Changelog 必须包含 `#15353` RSC CSRF 回移修复记录；
3. ChronoDesk 前端源码不得启用 unstable RSC API；
4. `npm audit --omit=dev` 若返回零漏洞则直接通过；
5. 在 GitHub 全局 Advisory 修正前，只接受
   `GHSA-qwww-vcr4-c8h2` 对 `react-router`/`react-router-dom` 形成的精确误报；
   任意新增或变化的生产依赖漏洞都会失败。

Dependabot 告警应以 `inaccurate` 处置，并引用上述官方回移修复与全局 Advisory
修正记录；这不是 `tolerable_risk`、`not_used` 或延期接受风险。

## 实施与验证

- 保持 `react-router` 与 `react-router-dom` 为精确版本 `7.18.2`，没有通过
  `overrides` 强塞不受 React Admin 支持的 v8。
- `npm ci` 可从干净依赖树重放 `ra-ui-materialui@5.15.1` 补丁；该补丁同时完整保留
  Material UI 9 Autocomplete 的 `input`、`inputLabel` 与 `htmlInput` slots。
- 新增只读浏览器回归，验证创建工单页的“工单类别”是可用 `combobox`、能够打开
  `listbox`，并且控制台与网络健康检查无异常。
- 已连接的真实 Chrome 完成登录、显式项目选择、工单列表、创建工单四个页签及类别
  下拉验证。类别下拉显示“Bug报告”“功能请求”“账户问题”“技术支持”，全流程
  `warn/error` 为 0；“创建工单”始终禁用，没有提交或写入工单。
- 隔离 Docker 环境的 Web 与 `/healthz` 均健康；精确 Playwright Chromium 回归、
  `npm run check` 与全仓 `make verify` 均通过。
- Dependabot #19 已于 `2026-08-02T01:48:29Z` 以 `inaccurate` 关闭，并附上官方
  v7 回移修复、项目 Advisory 正确范围及待合并全局元数据修正的说明。

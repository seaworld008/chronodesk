# ChronoDesk 项目绑定 SDK

本目录提供三种可直接编译或运行的最小客户端：

- `go/chronodesk`：Go 标准库客户端与 `httptest` 契约测试。
- `python/chronodesk`：Python 标准库客户端与 `unittest` 契约测试。
- `typescript`：基于标准 `fetch` 的 TypeScript 客户端与编译后契约测试。

三个客户端都在构造时强制绑定一个 `project_key`，所有 Agent REST 请求固定走
`/api/v2/projects/{projectKey}`。OAuth Client Credentials 还必须显式选择
`api`、`mcp` 或 `a2a` audience；SDK 不提供隐式默认值。

```bash
make install-sdk-deps
make test-sdk
```

`generator/` 只提供 Java 和 .NET 的 OpenAPI Generator 配置基线。当前仓库没有
提交它们的生成产物，也没有把生成后的 Java/.NET 客户端纳入编译矩阵，因此
它们不是受支持 SDK。完成生成物审查、项目绑定封装和消费者编译测试后，才可
提升为受支持状态。

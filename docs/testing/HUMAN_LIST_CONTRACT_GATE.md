# Human GET 与列表契约门禁

## 目的

Human OpenAPI 不能只检查文档自身。否则 Gin 新增了一个列表路由、但开发者忘记发布
契约时，文档内的数组检查仍会全部通过。

当前门禁从 `server/internal/` 的真实非测试 Go 源码扫描 `.GET(...)` 调用。扫描结果
使用稳定指纹：

```text
相对文件 | 函数 | 路由变量.GET | 路径表达式
```

指纹不包含行号和中间件，因此格式调整与中间件增删不会制造无意义变更。发现结果必须
在 `server/internal/routeinventory/manifest.go` 中明确分类为：

- `page`：目录分页，Human OpenAPI 必须提供 `page_size`，默认 25、最大 100；
- `cursor`：时间线分页，Human OpenAPI 必须提供 `limit`，默认 25、最大 100；
- `bounded`：单次响应内的集合必须声明不超过 100；
- `non-list`：Human 单资源、文件、聚合或 WebSocket 读取；
- `machine/public`：Agent REST、OAuth、A2A、健康检查和公开契约入口。

清单与源码执行双向比较。新增源码注册点没有分类会失败，删除注册点后留下清单也会
失败。`page`、`cursor` 和 `bounded` 必须绑定实际 Human OpenAPI Path 与
`operationId`；页式和游标式列表还必须声明以唯一 ID 结尾的稳定排序。

## 运行方式

```bash
cd server
go test ./internal/routeinventory ./internal/humanopenapi -count=1
go vet ./internal/routeinventory ./internal/humanopenapi
```

`make verify` 和 `go test ./...` 会自然包含这些测试，不需要修改生产路由启动逻辑。

## 新增 GET 路由时

1. 在真实 Gin 注册源码中添加路由；
2. 根据数据量语义在 manifest 中增加分类，不得用 `non-list` 隐藏目录或时间线；
3. Human 列表同步增加 OpenAPI operation、分页上限、默认值和稳定排序；
4. 为 Handler/Service 增加非法分页、边界值和稳定排序测试；
5. 运行上述门禁和生成类型 freshness 检查。

## 已知边界与 Map 集合

AST 门禁按注册点而不是运行时展开后的 RouteInfo 工作。像 OAuth
`router.GET(parsed.Path, ...)` 这样的循环动态路径会形成一个源码指纹；它被明确标为
`machine/public`。所有 Gin GET 必须继续放在 `server/internal/`，否则扫描器无法发现。

通用 OpenAPI 集合遍历目前可靠识别数组；它不会从 Go `map` 自动推断最大键数。
因此动态对象必须在领域实现中显式限量，并在发布 OpenAPI 时增加
`maxProperties`。当前高风险的分析维度 `ticket_stats.by_category` 使用
`AnalyticsMaxCategoryValues = 1000`，查询读取 `limit + 1`，超过上限返回
`ErrAnalyticsResultTooLarge`。以下两个测试共同锁定该约束：

- `humanopenapi.TestAnalyticsByCategoryMapHasAnExplicitRuntimeBound`
- `services.TestAnalyticsCategoryDimensionFailsClosedAtBound`

如果未来把 Analytics GET 发布进 Human OpenAPI，应同时为 `by_category` 增加
`maxProperties: 1000`，再把对应注册点从 `non-list` 调整为 `bounded`。

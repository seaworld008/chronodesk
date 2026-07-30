# Java 与 .NET 生成配置

这里的配置只用于评审 OpenAPI Generator 输出，不代表 Java 或 .NET SDK 已受
支持。运行时应从本目录调用生成器，使配置中的相对 `inputSpec` 指向权威
`server/internal/openapi/openapi.yaml`：

```bash
openapi-generator-cli generate -c java.yaml
openapi-generator-cli generate -c dotnet.yaml
```

生成输出必须留在 `dist/`（已由仓库忽略规则排除），不得手工修改生成
Schema。发布前仍需增加一层固定 `project_key`、显式 audience 的客户端封装，
并将 Maven/Gradle 与 `dotnet build` 消费者测试加入 CI；在这些门禁完成前，
Java/.NET 仅为生成配置，不是发布制品。

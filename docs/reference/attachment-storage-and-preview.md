# 附件存储、预览与 Agent 读取边界

## 目标与默认行为

ChronoDesk 将附件原件视为不可信私有对象。默认部署使用
`AGENT_ATTACHMENT_STORAGE_BACKEND=local`，原件写入服务器受控目录
`AGENT_ATTACHMENT_DIR`；对象键由系统生成，不包含用户文件名，也不位于
Web 根目录。数据库只保存逻辑对象键、权威 MIME、大小、SHA-256、扫描状态
和项目范围，不保存可公开访问的 URL。

上传始终经过以下链路：

1. Human 或 Service Principal 在 ProjectScope 内提交有界 multipart/body。
2. 内容先写入耐久暂存区并同步计算大小、SHA-256 和内容嗅探结果。
3. 数据库事务原子提交 Attachment、History、DomainEvent、Outbox 和审计。
4. Outbox Worker 在事务外将暂存对象迁移到当前正式存储。
5. 外部扫描器提交终态；只有 `clean` 对象可读取或预览。

网络对象存储不可用时，已提交的暂存对象仍可由 Outbox 安全重试，不会让
数据库事务跨越 S3 网络调用。

## 本地目录规划

单实例默认目录：

```text
./data/
├── agent-attachments/
│   └── tickets/{ticket_id}/{random_id}.{ext}
└── agent-attachment-staging/
    └── .staging/{random_id}.{ext}
```

目录权限由服务初始化为仅服务账号可访问，容器示例使用独立持久卷。生产
多副本部署必须把正式本地目录和暂存目录放在共享 RWX/PVC，并设置：

```dotenv
AGENT_ATTACHMENT_LOCAL_DEPLOYMENT_MODE=shared-rwx
CHRONODESK_REPLICA_COUNT=3
```

即使正式存储使用 S3，暂存仍是 Outbox 恢复边界；多副本时它同样必须共享。

## S3、MinIO 与云对象存储

正式存储可切换为 AWS S3、MinIO 或提供 S3 API 的云对象存储：

```dotenv
AGENT_ATTACHMENT_STORAGE_BACKEND=s3
AGENT_ATTACHMENT_LOCAL_STORE_ID=local-default
AGENT_ATTACHMENT_STAGING_DIR=/srv/chronodesk/attachment-staging
AGENT_ATTACHMENT_LOCAL_DEPLOYMENT_MODE=single

AGENT_ATTACHMENT_S3_ENDPOINT=https://minio.example.internal
AGENT_ATTACHMENT_S3_STORE_ID=s3-primary-2026
AGENT_ATTACHMENT_S3_REGION=us-east-1
AGENT_ATTACHMENT_S3_BUCKET=chronodesk-private
AGENT_ATTACHMENT_S3_PREFIX=chronodesk/attachments
AGENT_ATTACHMENT_S3_USE_PATH_STYLE=true
AGENT_ATTACHMENT_S3_ACCESS_KEY_ID=deploy-injected-value
AGENT_ATTACHMENT_S3_SECRET_ACCESS_KEY=deploy-injected-value
AGENT_ATTACHMENT_S3_SSE=bucket-default
AGENT_ATTACHMENT_S3_VERSIONING_MODE=auto
```

AWS S3 可留空 endpoint，并使用 AWS SDK 默认凭据链（工作负载身份、角色或
部署环境）。显式 Access Key 与 Secret 必须成对注入；凭据、桶地址和对象
URL 不进入数据库、前端响应、日志或 Agent 结果。

安全基线：

- 桶必须私有，阻止匿名读取和公共 ACL。
- 生产 endpoint 必须使用 HTTPS；开发 MinIO 的 HTTP 必须显式设置
  `AGENT_ATTACHMENT_S3_ALLOW_INSECURE=true`。
- `bucket-default` 依赖桶策略加密；也可选 `AES256` 或 `aws:kms`。
- 应用只持有指定 bucket/prefix 的 GetObject、PutObject、DeleteObject，
  以及启动检查所需的 HeadBucket、GetBucketVersioning 最小权限。
- 浏览器和 Agent 都通过 ChronoDesk 认证端点读取，不签发长期对象 URL。
- 每个正式存储使用稳定、非敏感、不可复用的 `store_id`。bucket、endpoint、
  prefix 和凭据仍不进入协议响应。
- 开启或暂停版本控制的桶必须在上传结果中返回 VersionID；后续读取和删除
  都绑定该精确版本。历史记录缺少 VersionID 时系统拒绝宣告物理删除成功。
- 大对象使用有界并发分片上传。

从本地切到 S3 时，新附件会持久化目标 `store_id`；上传 Outbox 即使在配置
切换后重放，也仍写入创建意图时冻结的 store。旧的 `local`/`s3` 记录仅在
该类型恰好对应一个已注册 store 时兼容读取；零个或多个候选都会 fail closed，
不会猜测 bucket。

切换 S3 bucket、prefix 或供应商时，必须给新存储分配新的 store_id，并把旧
配置放入最多 8 项的历史注册表，例如：

```dotenv
AGENT_ATTACHMENT_S3_STORE_ID=s3-primary-2026
AGENT_ATTACHMENT_S3_HISTORICAL_STORES_JSON=[{"store_id":"s3-primary-2025","endpoint":"https://old-objects.example.internal","region":"us-east-1","bucket":"chronodesk-old","prefix":"chronodesk/attachments","versioning_mode":"required"}]
```

所有历史 store 会在启动时同时执行 bucket 与版本控制检查；任一不可用会
阻止启动，避免上线后才发现旧件无法读取或清理。旧本地目录也应保持挂载，
直到离线迁移、数量/哈希核对和备份验证完成。不要直接复制后修改数据库；
迁移工具必须逐对象校验 `file_size + SHA-256` 后再切换引用。

## 第一阶段预览策略

预览只在用户主动点击后发起，不预加载附件正文。浏览器先通过原有项目授权
端点获取 Blob；关闭 Dialog、切换工单或取消请求时立即终止 fetch 并释放
Object URL。

| 类型 | 第一阶段行为 | 前端预览上限 |
|---|---|---:|
| JPEG、PNG、GIF、WebP、AVIF | 图片按需预览；SVG 不作为图片执行 | 15 MiB |
| MP3、WAV、Ogg、MP4、WebM | 原生控件，`preload=metadata`，禁止自动播放 | 25 MiB |
| PDF | sandbox iframe；浏览器不支持时回退下载 | 25 MiB |
| 纯文本、JSON、CSV | 仅文本节点渲染 | 1 MiB |
| Markdown | GFM；禁用 raw HTML、远程图片和活动元素 | 1 MiB |
| Office、HTML、SVG、归档和未知二进制 | 仅显示元数据并提供扫描后下载 | 不内联 |

客户端声明的 Content-Type 和 FileType 不可信。服务以暂存内容的嗅探结果为
权威 MIME；下载响应固定使用 `attachment`、`nosniff`、sandbox CSP 和私有
无缓存策略。当前完整性检查会读取完整对象并复算 SHA-256，因此本阶段不
声明 Range/206，避免“只校验分片”削弱现有完整性保证。

## Office、OCR 与派生内容

Office 转 PDF、PDF 文本提取、OCR、音频转写和视频摘要不能在上传 HTTP
事务或主 API 进程中同步执行。后续能力使用隔离 Worker：

- 仅处理当前版本且扫描为 `clean` 的原件。
- 派生任务幂等键包含 attachment id、SHA-256 和 pipeline version。
- Worker 无外网、非 root、只读根文件系统，并限制 CPU、内存、PID、临时
  磁盘、页数、像素、时长和输出字符数。
- 原件不可变；派生 PDF、缩略图和纯文本是单独对象，记录来源哈希和生成器
  版本。
- 解析失败、超时、加密文档或扫描器异常均 fail closed，不把状态改为 clean。

## AI Agent 读取

Agent REST 已支持具备 `attachments:read` Grant 和策略允许的 Service
Principal 列出附件元数据并读取扫描通过的原始字节。它复用 Human 下载的
ProjectScope、策略重验、SHA-256 完整性检查和审计边界，不获得桶凭据或对象
URL。

文档正文永远标记为不可信数据：Agent 不得把附件内容当成系统指令，也不得
因文档中的文字自动执行外部请求或高风险工具。后续向 MCP/A2A 暴露派生文本
时，应提供有界、版本化的只读资源，保留 attachment id、hash、页码/片段和
引用信息，并继续要求 `attachments:read`。

## 设计依据

- [OWASP File Upload Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/File_Upload_Cheat_Sheet.html)：类型白名单、大小限制、授权读取、Webroot 外存储、恶意软件扫描与文档净化。
- [AWS S3 Security Best Practices](https://docs.aws.amazon.com/AmazonS3/latest/userguide/security-best-practices.html)：关闭公共访问、禁用 ACL、最小权限、静态加密和 TLS。
- [MDN HTTP Range Requests](https://developer.mozilla.org/en-US/docs/Web/HTTP/Guides/Range_requests)：后续媒体分段读取所需的 206、Content-Range 与 416 契约。
- [Apache Tika Security Model](https://tika.apache.org/security-model.html)：解析器本身不是安全边界，文档解析应在隔离且资源受限的 Worker 中执行。

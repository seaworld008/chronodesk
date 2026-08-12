# 管理端导航登记规范

ChronoDesk 管理端侧栏是一棵连续的企业功能树。导航数据只在
`web/src/navigation/navigationRegistry.ts` 登记，渲染器不得为具体功能增加
条件分支。

## 当前一级顺序

1. 工作台
2. 项目运营
3. 智能运营
4. 集成中心
5. 项目设置
6. 治理中心
7. 系统设置

当前二级职责保持稳定：

- 工作台：运营大屏、跨项目工作台；
- 项目运营：项目概览、工单管理、知识库、项目通知；
- 智能运营：人机协作、自动化（仅规则与执行日志）、智能体管理；
- 集成中心：Webhook、集成运行；
- 项目设置：基本信息、项目成员、建单配置、SLA 策略、受理队列、工单模板、
  快捷回复、通知与外发；
- 治理中心：项目治理、平台身份与访问、审计中心；
- 系统设置：平台公共配置、邮件外发，以及后续接入的平台级安全、保留与清理设置。

功能按“用户任务”而不是后端模块名分组。运行期协作、执行和监控进入业务功能树；
改变当前项目行为的管理能力进入“项目设置”；改变所有项目默认值或平台
guardrail 的能力进入“系统设置”；跨项目资产、身份、合规和审计进入“治理中心”。
一个功能只能有一个 canonical 入口，其他旧路径仅作为受保护重定向存在。

一级节点使用相同视觉样式，不渲染“我的工作”“平台管理”等分区标题或分隔卡。
children 多于一个的一级节点点击只展开或收起二级列表；只有一个 child 时，通用
renderer 将一级节点直接渲染为该 child 的链接。进入路由时，所在的多子项分组默认
展开；用户之后仍可手动收起当前分组，各分组的展开选择彼此独立，也可以同时展开
多个分组。工作台当前依次包含“运营大屏”和“跨项目工作台”，因此由通用 renderer
渲染为 Collapse。`/workbench/dashboard` 使用更具体的 active prefix；
`/workbench` leaf 通过 registry 的 `activePathExclusions` 排除该子路由，避免两个
入口同时激活。

功能树的状态视觉与 OINK 文档导航保持同一合同：一级文字使用 `#16222e`，二级
文字使用 `#586b80`，非当前项 hover 使用 `rgba(22, 34, 46, 0.06)`；当前功能页
使用 `#245f94`、`rgba(36, 95, 148, 0.1)` 背景和 1px 当前项导轨。分组展开本身
不冒充当前功能页，当前页只由 canonical route 决定。桌面行高 36px、圆角 10px；
移动端行高保持 44px，以满足触控命中要求。

桌面侧栏展开宽度默认 240px，可在 216–360px 之间拖动调整；分隔条支持
Left/Right、Shift 加速、Home/End 和双击恢复默认值，宽度偏好按账号持久化。
收起宽度固定为 56px，只保留带可访问名称和 Tooltip 的一级图标，二级功能、文字、
箭头和导轨立即退出渲染，但不会清空各分组的展开选择；点击收起态的分组图标会恢复
上次宽度和树状态。移动端继续使用 240px 临时 Drawer，不显示桌面调宽分隔条，也
不会覆盖桌面宽度偏好。

项目运营、智能运营、集成中心和项目设置都要求已解析的 `ProjectScope`。没有
当前项目时，这些分组整体隐藏。治理中心只承载已经实现的项目治理、平台身份与访问、
审计入口；系统设置作为独立一级，只承载真实的平台公共配置、邮件外发等
路由。平台角色不能推导项目 scope 或项目职责。

平台级“安全与应急”使用独立接口、严格 `emergency_operator` 角色、
`ETag/If-Match` 并发控制、拒绝审计与失败关闭运行时快照；它不复用项目级
智能体开关，也不会向平台管理员或审计员显示。

SLA、工单模板和快捷回复会改变当前项目的受理行为，因此 canonical 路径属于
`/project-settings/*`；旧 `/automation-*` 路径只保留一个版本的受保护重定向。
自动化页面只呈现规则和执行日志，避免把服务台配置误归类为自动化运行能力。
知识搜索、阅读和“从工单沉淀方案”属于日常运营，因此 canonical 路径为
`/knowledge`；旧 `/project-settings/knowledge` 只做受保护重定向。未来项目级
知识策略可以进入项目设置，但不能复制日常知识入口。

## 节点模板

registry 使用 `kind: 'group' | 'leaf'` 判别联合。当前 UX 最多两层，group 只能
包含 leaf。每个 leaf 的 `route` 同时声明 `existing` 或 custom component mapping；
custom route、guard 和旧 URL redirect 都从同一 contract 生成。

```ts
{
    kind: 'group',
    id: 'project-example',
    label: '项目示例',
    icon: 'settings',
    order: 70,
    scope: 'project',
    capability: null,
    roles: null,
    placement: 'sidebar',
    path: null,
    children: [
        {
            kind: 'leaf',
            id: 'project-example-list',
            label: '示例列表',
            icon: 'settings',
            order: 10,
            scope: 'project',
            capability: {
                kind: 'project',
                value: 'manage_integrations',
            },
            roles: null,
            placement: 'sidebar',
            path: '/project-examples',
            activePathPrefixes: ['/project-examples'],
            route: { kind: 'existing' },
        },
    ],
}
```

平台节点使用 `scope: 'platform'` 和平台 capability/roles。账号节点登记在
`placement: 'account'` 的账号 group 下，不进入侧栏。每个节点必须提供稳定且
全局唯一的 `id`；leaf 的 canonical `path` 也必须唯一。

新增功能必须遵循以下顺序，不允许直接在渲染器或 `AdminApp` 中手写菜单：

1. 判断功能属于 global、project、platform 还是 account 范围；
2. 选择唯一一级任务域，复用既有 group；只有形成长期独立任务域时才能新增 group；
3. 在 registry 新增 leaf，声明稳定 ID、顺序、范围、能力、角色、路由与旧路径；
4. custom 页面补充 exhaustive component mapping，React Admin Resource 补充
   `resourceAccessContracts`；
5. 添加 validator、角色矩阵、路由作用域、展开状态和旧路径测试；
6. 更新本文件的二级职责清单。不存在受保护后端能力的功能不得先挂占位菜单。

一级最多保持七个稳定任务域，二级使用名词或用户任务命名，避免“管理中心”“其他”
“更多”等兜底分类。某一级只有一个可见 child 时，通用渲染器自动直达；以后增加
child 后会自然变为可展开树，不需要改页面代码。`order` 每级以 10 为间隔，便于
后续插入，但产品顺序仍由 registry validator 和测试固定。

## 路由和权限边界

registry 同时提供入口可见性与 custom route guard contract，route guard 始终是
授权权威。新增 custom 页面只登记 leaf 的 path/scope/capability/roles/route，
并在 exhaustive component map 提供组件，不能因为平台角色显示入口而构造
`ProjectScope`。旧 URL 写在同一 leaf 的 `legacyPaths`，由相同 guard 生成
`Navigate replace`，保留一个版本，不在 renderer 写别名判断。React Admin
Resource 仍由框架注册，但测试必须逐项核对其 registry contract。

展开状态使用版本化 key，并绑定现有认证会话公开的 `subject + session_id`。合法
group ID 始终来自完整 registry，而非异步权限过滤后的首屏子集；换账号、换会话
不会复用，已删除的 group ID 会在读取和写入时丢弃，新增 group 与旧状态安全合并。

## 自动校验与测试

`validateNavigationRegistry` 会拒绝：

- 重复 ID 或 canonical path；
- 非法 scope、placement 或 capability/roles 与 scope 不匹配；
- 循环、超过两层的 group、空 group；
- leaf 携带 children、group 携带跳转 path；
- 缺失 active path 的 leaf。

每次新增节点至少覆盖：职责/capability 可见性、无项目隐藏、route guard、当前
路由进入时默认展开且可手动收起、Enter/Space 键盘操作、
`aria-expanded`/`aria-controls`、会话隔离持久化，以及旧 URL 重定向。新增 leaf
的测试应证明只修改 registry 数据即可由通用 renderer 呈现。侧栏壳层改动还必须
覆盖收起态无可见文字和二级节点、宽度边界与刷新恢复、separator 键盘操作，以及
移动端无调宽控件。

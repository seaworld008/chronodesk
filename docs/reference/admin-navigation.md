# 管理端导航登记规范

ChronoDesk 管理端侧栏是一棵连续的企业功能树。导航数据只在
`web/src/navigation/navigationRegistry.ts` 登记，渲染器不得为具体功能增加
条件分支。

## 当前一级顺序

1. 工作台
2. 项目运营
3. 智能运营
4. 集成中心
5. 项目配置
6. 治理中心

一级节点使用相同视觉样式，不渲染“我的工作”“平台管理”等分区标题或分隔卡。
一级节点点击只展开或收起二级列表；当前路由所在分组会强制保持展开，同时保留用户
手动展开的其他分组。当前只有一个“跨项目工作台”子项，后续运营大屏实现后只需
在该分组登记新的 leaf，不能提前登记死链。

项目运营、智能运营、集成中心和项目配置都要求已解析的 `ProjectScope`。没有
当前项目时，这些分组整体隐藏。治理中心只承载平台面功能；平台角色不能推导项目
scope 或项目职责。

## 节点模板

registry 使用 `kind: 'group' | 'leaf'` 判别联合。当前 UX 最多两层，group 只能
包含 leaf。

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
        },
    ],
}
```

平台节点使用 `scope: 'platform'` 和平台 capability/roles。账号节点登记在
`placement: 'account'` 的账号 group 下，不进入侧栏。每个节点必须提供稳定且
全局唯一的 `id`；leaf 的 canonical `path` 也必须唯一。

## 路由和权限边界

registry 只决定入口是否可见，route guard 始终是授权权威。新增入口时必须同步
登记受保护 route，且不能因为平台角色显示入口而构造 `ProjectScope`。旧 URL
兼容应在 route 层做受相同 capability 保护的 `Navigate replace`，保留一个版本，
不要在 renderer 写别名判断。

展开状态使用版本化 key，并绑定现有认证会话公开的 `subject + session_id`。只
持久化当前 registry 中合法的 group ID；换账号、换会话不会复用，已删除的 group
ID 会在读取和写入时丢弃。

## 自动校验与测试

`validateNavigationRegistry` 会拒绝：

- 重复 ID 或 canonical path；
- 非法 scope、placement 或 capability/roles 与 scope 不匹配；
- 循环、超过两层的 group、空 group；
- leaf 携带 children、group 携带跳转 path；
- 缺失 active path 的 leaf。

每次新增节点至少覆盖：职责/capability 可见性、无项目隐藏、route guard、当前
路由自动展开、Enter/Space 键盘操作、`aria-expanded`/`aria-controls`、会话隔离
持久化，以及旧 URL 重定向。新增 leaf 的测试应证明只修改 registry 数据即可由
通用 renderer 呈现。

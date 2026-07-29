# ChronoDesk CloudEvents 1.0

ChronoDesk 的领域事件采用 CloudEvents `1.0` 结构化 JSON 信封，规范基线为
CloudEvents `v1.0.2`。

## 对外信封

核心属性为 `specversion`、`id`、`source`、`type`、`subject`、`time`、
`datacontenttype`、`dataschema` 和 `data`。

扩展上下文属性遵循 CloudEvents 命名与类型系统，只使用小写字母或数字和
标量值：

- `traceid`
- `correlationid`
- `causationid`
- `actortype`
- `actorid`
- `resourceversion`

完整的 `ActorRef` 同时放在 `data.actor`。数据库列继续使用
`trace_id`、`actor_type`、`resource_version` 等内部名称；这些列名不会
出现在对外 CloudEvent 信封中。

消费者必须使用 `(source, id)` 去重，并将未知扩展属性视为可忽略元数据。
完整 Schema 以
[`/openapi.yaml`](../../server/internal/openapi/openapi.yaml) 为准。

## 自动化规则触发

`AutomationRule.trigger_event` 直接保存并精确匹配 `CloudEvent.type`，不会再把
一个领域事件扩展成 `ticket.created`、`ticket.updated` 等内部别名。当前管理
界面可选择：

- `io.chronodesk.ticket.created.v1`
- `io.chronodesk.ticket.updated.v1`
- `io.chronodesk.ticket.assigned.v1`
- `io.chronodesk.ticket.transitioned.v1`
- `io.chronodesk.ticket.escalated.v1`
- `io.chronodesk.ticket.comment.created.v1`
- `io.chronodesk.ticket.attachment.created.v1`
- `io.chronodesk.ticket.sla.breached.v1`
- `io.chronodesk.automation.trigger.requested.v1`

解决、关闭和重新打开均属于 `ticket.transitioned`；规则通过 `status` 条件区分
目标状态。标准数据库迁移会幂等升级已知旧值，并为原 `ticket.resolved` 与
`ticket.closed` 规则补充对应状态条件；遇到未知值则停止迁移，避免规则静默
失效。

## Webhook 订阅与投递

`WebhookConfig.enabled_events` 直接保存完整 `CloudEvent.type`，Outbox 按精确类型
查找订阅并将同一类型写入 `WebhookLog.event_type`。系统不再把领域事件降级为
`ticket.created`、`ticket.updated`、`system.alert` 等短名称。当前可订阅类型：

- `io.chronodesk.ticket.created.v1`
- `io.chronodesk.ticket.updated.v1`
- `io.chronodesk.ticket.assigned.v1`
- `io.chronodesk.ticket.transitioned.v1`
- `io.chronodesk.ticket.escalated.v1`
- `io.chronodesk.ticket.comment.created.v1`
- `io.chronodesk.ticket.attachment.created.v1`
- `io.chronodesk.ticket.sla.breached.v1`
- `io.chronodesk.ticket.deleted.v1`
- `io.chronodesk.automation.notification.requested.v1`
- `io.chronodesk.system.alert.v1`

解决、关闭及其他状态变化都投递
`io.chronodesk.ticket.transitioned.v1`。仅订阅特定目标状态时，在
`filter_rules.transition_statuses` 中使用 `open`、`in_progress`、`pending`、
`resolved`、`closed` 或 `cancelled`；留空表示全部状态。筛选只在事件类型精确
匹配后执行，缺少目标状态的数据不会命中受筛选的订阅。

标准迁移会幂等地保留旧订阅的投递语义。例如旧 `ticket.updated` 会展开为内容
更新、附件新增及非解决/关闭的状态流转，旧 `ticket.resolved` 和
`ticket.closed` 会迁移为状态流转加明确谓词，旧 `system.alert` 会覆盖原先实际
投递的 SLA 违约与当前系统警报类型。没有发布方的 `user.registered` 订阅会被
移除；配置若因此没有任何事件，将自动停用。未知配置值或无法诚实映射的历史
日志会使迁移回滚并报错，不会静默放宽订阅或伪造事件类型。

## 官方依据

- [CloudEvents v1.0.2 核心规范](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md)
- [CloudEvents JSON 格式](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/formats/json-format.md)

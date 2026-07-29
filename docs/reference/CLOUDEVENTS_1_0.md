# ChronoDesk CloudEvents 1.0

ChronoDesk 的领域事件采用 CloudEvents `1.0` 结构化 JSON 信封；当前兼容
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

## 官方依据

- [CloudEvents v1.0.2 核心规范](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md)
- [CloudEvents JSON 格式](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/formats/json-format.md)

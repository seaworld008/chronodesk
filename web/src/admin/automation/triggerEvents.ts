export const automationTriggerEventChoices = [
  { id: 'io.chronodesk.ticket.created.v1', name: '工单创建' },
  { id: 'io.chronodesk.ticket.updated.v1', name: '工单更新' },
  { id: 'io.chronodesk.ticket.assigned.v1', name: '工单分配' },
  { id: 'io.chronodesk.ticket.transitioned.v1', name: '工单状态流转' },
  { id: 'io.chronodesk.ticket.escalated.v1', name: '工单升级' },
  { id: 'io.chronodesk.ticket.comment.created.v1', name: '新增工单评论' },
  { id: 'io.chronodesk.ticket.attachment.created.v1', name: '新增工单附件' },
  { id: 'io.chronodesk.ticket.sla.breached.v1', name: '工单 SLA 违约' },
  { id: 'io.chronodesk.automation.trigger.requested.v1', name: '定时检查' },
]

export const automationTriggerEventLabel = (eventType?: string) =>
  automationTriggerEventChoices.find((choice) => choice.id === eventType)?.name ?? '未知事件'

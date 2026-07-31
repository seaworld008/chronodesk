import {
    AutocompleteInput,
    Create,
    ReferenceInput,
    SelectInput,
    SimpleForm,
    TextInput,
    required,
} from 'react-admin'
import type {
    CreateNotificationRequest,
    NotificationChannel,
    NotificationPriority,
    NotificationType,
} from '@/lib/generated/human-api'

const notificationTypeChoices = [
    { id: 'ticket_assigned', name: '工单分配' },
    { id: 'ticket_status_changed', name: '状态变更' },
    { id: 'ticket_commented', name: '评论通知' },
    { id: 'ticket_created', name: '工单创建' },
    { id: 'ticket_overdue', name: '工单逾期' },
    { id: 'ticket_resolved', name: '工单解决' },
    { id: 'ticket_closed', name: '工单关闭' },
    { id: 'system_maintenance', name: '系统维护' },
    { id: 'user_mention', name: '用户提及' },
    { id: 'system_alert', name: '系统警报' },
] as const

const priorityChoices = [
    { id: 'low', name: '低' },
    { id: 'normal', name: '普通' },
    { id: 'high', name: '高' },
    { id: 'urgent', name: '紧急' },
] as const

const channelChoices = [
    { id: 'in_app', name: '应用内' },
    { id: 'email', name: '邮件' },
    { id: 'webhook', name: 'Webhook' },
    { id: 'websocket', name: 'WebSocket' },
] as const

const choiceIDs = <T extends string>(
    choices: readonly { id: T }[],
): ReadonlySet<string> => new Set(choices.map(({ id }) => id))

const notificationTypes = choiceIDs(notificationTypeChoices)
const notificationPriorities = choiceIDs(priorityChoices)
const notificationChannels = choiceIDs(channelChoices)

const requiredString = (value: unknown, field: string): string => {
    if (typeof value !== 'string' || value.trim() === '') {
        throw new Error(`${field}不能为空`)
    }
    return value.trim()
}

const exactChoice = <T extends string>(
    value: unknown,
    values: ReadonlySet<string>,
    field: string,
): T => {
    if (typeof value !== 'string' || !values.has(value)) {
        throw new Error(`${field}无效`)
    }
    return value as T
}

const transformCreateNotification = (
    value: Record<string, unknown>,
): CreateNotificationRequest => {
    const recipientID = Number(value.recipient_id)
    if (!Number.isSafeInteger(recipientID) || recipientID <= 0) {
        throw new Error('接收者无效')
    }
    return {
        type: exactChoice<NotificationType>(
            value.type,
            notificationTypes,
            '通知类型',
        ),
        title: requiredString(value.title, '标题'),
        content: requiredString(value.content, '内容'),
        priority: exactChoice<NotificationPriority>(
            value.priority,
            notificationPriorities,
            '优先级',
        ),
        channel: exactChoice<NotificationChannel>(
            value.channel,
            notificationChannels,
            '渠道',
        ),
        recipient_id: recipientID,
    }
}

const NotificationCreate = () => (
    <Create
        title="创建通知"
        redirect="list"
        transform={transformCreateNotification}
    >
        <SimpleForm
            defaultValues={{
                type: 'system_alert',
                priority: 'normal',
                channel: 'in_app',
            }}
        >
            <SelectInput
                source="type"
                label="通知类型"
                choices={[...notificationTypeChoices]}
                validate={required()}
                fullWidth
            />
            <TextInput
                source="title"
                label="标题"
                validate={required()}
                fullWidth
            />
            <TextInput
                source="content"
                label="内容"
                validate={required()}
                multiline
                minRows={4}
                fullWidth
            />
            <SelectInput
                source="priority"
                label="优先级"
                choices={[...priorityChoices]}
                validate={required()}
                fullWidth
            />
            <SelectInput
                source="channel"
                label="渠道"
                choices={[...channelChoices]}
                validate={required()}
                fullWidth
            />
            <ReferenceInput
                source="recipient_id"
                reference="assignees"
                label="接收者"
            >
                <AutocompleteInput
                    label="接收者"
                    optionText="username"
                    validate={required()}
                    fullWidth
                />
            </ReferenceInput>
        </SimpleForm>
    </Create>
)

export default NotificationCreate

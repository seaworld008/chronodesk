import React from 'react';
import {
    Create,
    TextInput,
    SelectInput,
    DateTimeInput,
    ReferenceInput,
    AutocompleteInput,
    required,
    TopToolbar,
    ListButton,
    SaveButton,
    TabbedForm,
    FormTab,
    BooleanInput,
    NumberInput,
} from 'react-admin';
import {
    Box,
    Typography,
    Card,
    CardContent,
    CardHeader,
    Alert,
    AlertTitle,
} from '@mui/material';
import { minCharacters, maxCharacters } from '@/lib/validators';
import {
    normalizeTagsForSubmit,
    formatTagsInputValue,
    normalizeStringArrayForSubmit,
    normalizeCustomFieldsForSubmit,
} from './tagUtils';
import BackButton from '../common/BackButton';
import { CreateTicketRequest, TicketStatus } from '@/types';

// 选项配置
const statusChoices = [
    { id: 'open', name: '待处理' },
    { id: 'in_progress', name: '处理中' },
    { id: 'pending', name: '等待中' },
    { id: 'resolved', name: '已解决' },
    { id: 'closed', name: '已关闭' },
];

const priorityChoices = [
    { id: 'low', name: '低' },
    { id: 'normal', name: '普通' },
    { id: 'high', name: '高' },
    { id: 'urgent', name: '紧急' },
    { id: 'critical', name: '严重' },
];

const sourceChoices = [
    { id: 'web', name: '网页' },
    { id: 'email', name: '邮件' },
    { id: 'phone', name: '电话' },
    { id: 'chat', name: '聊天' },
    { id: 'api', name: 'API' },
    { id: 'mobile', name: '移动端' },
];

const typeChoices = [
    { id: 'incident', name: '事件' },
    { id: 'request', name: '请求' },
    { id: 'problem', name: '问题' },
    { id: 'change', name: '变更' },
    { id: 'complaint', name: '投诉' },
    { id: 'consultation', name: '咨询' },
];

// 表单验证
const validateTitle = [
    required(),
    minCharacters(5, '至少输入 5 个字符'),
    maxCharacters(200, '不能超过 200 个字符'),
];

const validateDescription = [
    required(),
    minCharacters(10, '请提供至少 10 个字符的描述'),
    maxCharacters(2000, '不能超过 2000 个字符'),
];

// 默认值
const defaultValues = {
    status: 'open',
    priority: 'normal',
    source: 'web',
    type: 'request',
    is_private: false,
    estimated_hours: 1,
};

type TicketCreateFormValues = CreateTicketRequest & {
    status?: TicketStatus;
    is_private?: boolean;
    estimated_hours?: number;
    tags?: unknown;
    attachments?: unknown;
    custom_fields?: unknown;
    [key: string]: unknown;
};

const transformTicketCreate = (data: TicketCreateFormValues): Record<string, unknown> => {
    const payload: Record<string, unknown> = { ...data };
    const normalizedTags = normalizeTagsForSubmit(data.tags);

    if (typeof normalizedTags !== 'undefined') {
        payload.tags = normalizedTags;
    } else {
        delete payload.tags;
    }

    const normalizedAttachments = normalizeStringArrayForSubmit(data.attachments);
    if (typeof normalizedAttachments !== 'undefined') {
        payload.attachments = normalizedAttachments;
    } else {
        delete payload.attachments;
    }

    const normalizedCustomFields = normalizeCustomFieldsForSubmit(data.custom_fields);
    if (typeof normalizedCustomFields !== 'undefined') {
        payload.custom_fields = normalizedCustomFields;
    } else {
        delete payload.custom_fields;
    }

    return payload;
};

/**
 * 自定义工具栏
 */
const TicketCreateToolbar = () => (
    <Box sx={{ display: 'flex', justifyContent: 'space-between', p: 2 }}>
        <SaveButton 
            label="创建工单" 
            variant="contained"
            size="large"
        />
    </Box>
);

/**
 * 创建工单操作按钮
 */
const TicketCreateActions = () => (
    <TopToolbar>
        <ListButton label="返回列表" />
    </TopToolbar>
);

/**
 * 创建工单页面
 */
const TicketCreate: React.FC = () => {
    return (
        <Box sx={{ p: 3 }}>
            <BackButton />
            <Alert severity="info" sx={{ mb: 3 }}>
                <AlertTitle>创建新工单</AlertTitle>
                <Typography variant="body2">
                    请填写工单的详细信息。工单创建后将自动分配给指定的负责人。
                </Typography>
            </Alert>

            <Create 
                actions={<TicketCreateActions />}
                title="创建新工单"
                mutationMode="pessimistic"
                redirect="show"
                transform={transformTicketCreate}
            >
                <TabbedForm 
                    toolbar={<TicketCreateToolbar />} 
                    syncWithLocation={false}
                    defaultValues={defaultValues}
                >
                    {/* 基本信息 */}
                    <FormTab label="基本信息" path="">
                        <Card>
                            <CardHeader title="工单基本信息" />
                            <CardContent>
                                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                    <TextInput
                                        source="title"
                                        label="工单标题"
                                        validate={validateTitle}
                                        fullWidth
                                        required
                                        helperText="简洁明了地描述问题或需求，将作为工单的主要标识"
                                    />
                                    
                                    <TextInput
                                        source="description"
                                        label="详细描述"
                                        validate={validateDescription}
                                        fullWidth
                                        required
                                        multiline
                                        rows={6}
                                        helperText="详细描述问题的现象、影响范围、期望的解决方案等"
                                    />

                                    <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                        <Box sx={{ flex: 1, minWidth: '200px' }}>
                                            <SelectInput
                                                source="priority"
                                                label="优先级"
                                                choices={priorityChoices}
                                                defaultValue="normal"
                                                fullWidth
                                                required
                                                helperText="根据问题的紧急程度选择合适的优先级"
                                            />
                                        </Box>

                                        <Box sx={{ flex: 1, minWidth: '200px' }}>
                                            <SelectInput
                                                source="status"
                                                label="初始状态"
                                                choices={statusChoices}
                                                defaultValue="open"
                                                fullWidth
                                                required
                                                helperText="工单的初始状态，通常为待处理"
                                            />
                                        </Box>

                                        <Box sx={{ flex: 1, minWidth: '200px' }}>
                                            <SelectInput
                                                source="source"
                                                label="来源"
                                                choices={sourceChoices}
                                                defaultValue="web"
                                                fullWidth
                                                required
                                                helperText="标记工单来源渠道"
                                            />
                                        </Box>
                                    </Box>

                                    <ReferenceInput source="assignee_id" reference="users" label="分配给">
                                        <AutocompleteInput 
                                            optionText={(choice) => `${choice.username} (${choice.first_name} ${choice.last_name})`}
                                            fullWidth
                                            helperText="选择负责处理此工单的人员"
                                        />
                                    </ReferenceInput>

                                    <TextInput 
                                        source="tags" 
                                        label="标签" 
                                        fullWidth 
                                        helperText="用逗号分隔多个标签，便于分类和搜索" 
                                        format={formatTagsInputValue}
                                    />
                                </Box>
                            </CardContent>
                        </Card>
                    </FormTab>

                    {/* 分类信息 */}
                    <FormTab label="分类与类型" path="category">
                        <Card>
                            <CardHeader title="工单分类" />
                            <CardContent>
                                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                    <ReferenceInput source="category_id" reference="categories" label="工单类别" >
                                        <AutocompleteInput 
                                            optionText="name"
                                            fullWidth
                                            helperText="选择工单所属的主要类别"
                                        />
                                    </ReferenceInput>

                                    <SelectInput
                                        source="type"
                                        label="工单类型"
                                        choices={typeChoices}
                                        defaultValue="request"
                                        fullWidth
                                        required
                                        helperText="进一步细化工单的具体类型，如Bug报告、功能请求、技术支持等"
                                    />

                                    <ReferenceInput source="product_id" reference="products" label="相关产品">
                                        <AutocompleteInput 
                                            optionText="name"
                                            fullWidth
                                            helperText="选择此工单相关的产品或服务"
                                        />
                                    </ReferenceInput>

                                    <TextInput
                                        source="component"
                                        label="组件/模块"
                                        fullWidth
                                        helperText="涉及的具体组件或模块名称"
                                    />
                                </Box>
                            </CardContent>
                        </Card>
                    </FormTab>

                    {/* 时间与SLA */}
                    <FormTab label="时间管理" path="timeline">
                        <Card>
                            <CardHeader title="时间与SLA设置" />
                            <CardContent>
                                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                    <DateTimeInput
                                        source="due_date"
                                        label="预期完成时间"
                                        fullWidth
                                        helperText="期望解决此工单的最晚时间"
                                    />

                                    <NumberInput
                                        source="estimated_hours"
                                        label="预估工时"
                                        defaultValue={1}
                                        fullWidth
                                        min={0}
                                        step={0.5}
                                        helperText="预估解决此工单需要的工作时间（小时）"
                                    />

                                    <NumberInput
                                        source="sla_hours"
                                        label="SLA时间（小时）"
                                        fullWidth
                                        min={1}
                                        helperText="服务级别协议规定的响应/解决时间"
                                    />

                                    <BooleanInput
                                        source="is_urgent"
                                        label="标记为紧急"
                                        helperText="勾选后将在工单列表中突出显示"
                                    />
                                </Box>
                            </CardContent>
                        </Card>
                    </FormTab>

                    {/* 附加信息 */}
                    <FormTab label="附加设置" path="advanced">
                        <Card>
                            <CardHeader title="高级设置" />
                            <CardContent>
                                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                    <BooleanInput
                                        source="is_private"
                                        label="私有工单"
                                        helperText="私有工单仅对创建者和分配的负责人可见"
                                    />

                                    <BooleanInput
                                        source="is_internal"
                                        label="内部工单"
                                        helperText="内部工单不会发送给客户，仅供内部处理使用"
                                    />

                                    <TextInput
                                        source="external_reference"
                                        label="外部参考号"
                                        fullWidth
                                        helperText="关联的外部系统工单号或参考编号"
                                    />

                                    <ReferenceInput source="parent_ticket_id" reference="tickets" label="父工单">
                                        <AutocompleteInput 
                                            optionText="title"
                                            fullWidth
                                            helperText="如果这是子工单，选择对应的父工单"
                                        />
                                    </ReferenceInput>

                                    <TextInput
                                        source="resolution_notes"
                                        label="解决方案预案"
                                        fullWidth
                                        multiline
                                        rows={3}
                                        helperText="预期的解决思路或方案，可后续修改"
                                    />
                                </Box>
                            </CardContent>
                        </Card>
                    </FormTab>
                </TabbedForm>
            </Create>
        </Box>
    );
};

export default TicketCreate;

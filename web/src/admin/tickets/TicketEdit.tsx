import React from 'react';
import {
    Edit,
    TextInput,
    SelectInput,
    DateTimeInput,
    ReferenceInput,
    AutocompleteInput,
    required,
    TopToolbar,
    ListButton,
    ShowButton,
    SaveButton,
    TabbedForm,
    FormTab,
    useGetIdentity,
    usePermissions,
    useRecordContext,
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
    formatTagsInputValue,
    formatCustomFieldsInputValue,
    validateCustomFieldsInput,
} from './tagUtils';
import BackButton from '../common/BackButton';
import {
    canDeleteTicket,
    canEditTicket,
    type TicketAccessRecord,
    type TicketRolePermissions,
} from './ticketAccess';
import { FocusSafeDeleteButton } from '@/components/actions/FocusSafeDeleteButtons';
import {
    parseProjectRole,
} from '@/lib/projectScope';
import { transformTicketUpdate } from './ticketTransforms';

// 状态选项
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
];

/**
 * 工单编辑操作按钮
 */
const TicketEditActions = () => {
    const { permissions } = usePermissions<TicketRolePermissions>();
    return (
        <TopToolbar>
            <ShowButton label="查看详情" />
            <ListButton label="返回列表" />
            {canDeleteTicket(permissions?.project_role) && (
                <FocusSafeDeleteButton label="删除" mutationMode="pessimistic" />
            )}
        </TopToolbar>
    );
};

const TicketEditAuthorization = ({ children }: React.PropsWithChildren) => {
    const record = useRecordContext<TicketAccessRecord>();
    const { permissions, isPending: permissionsPending } = usePermissions<TicketRolePermissions>();
    const { identity, isPending: identityPending } = useGetIdentity();

    if (permissionsPending || identityPending || !record) {
        return null;
    }
    if (!canEditTicket(record, permissions?.project_role, identity?.id)) {
        return (
            <Alert severity="warning" sx={{ m: 2 }}>
                <AlertTitle>当前工单为只读</AlertTitle>
                只有工单创建者、当前处理人或管理角色可以修改此工单。
                <Box sx={{ mt: 1 }}>
                    <ShowButton label="查看工单详情" />
                </Box>
            </Alert>
        );
    }
    return <>{children}</>;
};

/**
 * 自定义保存工具栏
 */
const TicketEditToolbar = () => (
    <Box sx={{ display: 'flex', justifyContent: 'space-between', p: 2 }}>
        <SaveButton label="保存更改" />
    </Box>
);


/**
 * 编辑工单页面
 */
const TicketEdit: React.FC = () => {
    const { permissions } = usePermissions<TicketRolePermissions>();
    const projectRole = parseProjectRole(permissions?.project_role);
    const requester = projectRole === 'requester';

    return (
        <Box sx={{ p: 3 }}>
            <BackButton />
            <Edit
                actions={<TicketEditActions />}
                title="编辑工单"
                mutationMode="pessimistic"
                transform={(data) =>
                    transformTicketUpdate(
                        data,
                        projectRole,
                    )
                }
            >
                <TicketEditAuthorization>
                    <TabbedForm
                        toolbar={<TicketEditToolbar />}
                        syncWithLocation={false}
                    >
                    {/* 基本信息 */}
                    <FormTab label="基本信息" path="">
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <Alert severity="info" sx={{
                                borderRadius: 2,
                                backgroundColor: '#eff6ff',
                                color: '#1e40af',
                                border: '1px solid #dbeafe',
                                '& .MuiAlert-icon': { color: '#2563eb' }
                            }}>
                                <AlertTitle sx={{ fontWeight: 600 }}>编辑工单信息</AlertTitle>
                                <Typography variant="body2">
                                    修改工单的基本信息，更改后会记录到工单历史中。
                                </Typography>
                            </Alert>

                            <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
                                <CardHeader
                                    title="基本信息"
                                    slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                                    sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
                                />
                                <CardContent>
                                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                        <TextInput
                                            source="title"
                                            label="工单标题"
                                            validate={validateTitle}
                                            fullWidth
                                            required
                                        />

                                        <TextInput
                                            source="description"
                                            label="详细描述"
                                            validate={validateDescription}
                                            fullWidth
                                            required
                                            multiline
                                            rows={6}
                                        />

                                        <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                            {!requester && (
                                                <Box sx={{ flex: 1, minWidth: '200px' }}>
                                                    <SelectInput
                                                        source="status"
                                                        label="状态"
                                                        choices={statusChoices}
                                                        required
                                                    />
                                                </Box>
                                            )}

                                            <Box sx={{ flex: 1, minWidth: '200px' }}>
                                                <SelectInput
                                                    source="priority"
                                                    label="优先级"
                                                    choices={priorityChoices}
                                                    required
                                                />
                                            </Box>

                                            <Box sx={{ flex: 1, minWidth: '200px' }}>
                                                <SelectInput
                                                    source="source"
                                                    label="来源"
                                                    choices={sourceChoices}
                                                    required
                                                />
                                            </Box>

                                            <Box sx={{ flex: 1, minWidth: '200px' }}>
                                                <SelectInput
                                                    source="type"
                                                    label="类型"
                                                    choices={typeChoices}
                                                    required
                                                />
                                            </Box>
                                        </Box>
                                    </Box>
                                </CardContent>
                            </Card>
                        </Box>
                    </FormTab>

                    {/* 分配和分类 */}
                    <FormTab
                        label={requester ? '分类' : '分配和分类'}
                        path="assignment"
                    >
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
                                <CardHeader
                                    title={requester ? '工单分类' : '工单分配'}
                                    slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                                    sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
                                />
                                <CardContent>
                                    <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                        {!requester && (
                                            <Box sx={{ flex: 1, minWidth: '250px' }}>
                                                <ReferenceInput
                                                    source="assigned_to_id"
                                                    reference="assignees"
                                                    label="分配给"
                                                >
                                                    <AutocompleteInput
                                                        label="分配给"
                                                        optionText="username"
                                                        optionValue="id"
                                                        helperText="选择负责处理此工单的用户"
                                                    />
                                                </ReferenceInput>
                                            </Box>
                                        )}

                                        <Box sx={{ flex: 1, minWidth: '250px' }}>
                                            <ReferenceInput
                                                source="category_id"
                                                reference="categories"
                                                label="工单分类"
                                            >
                                                <AutocompleteInput
                                                    label="工单分类"
                                                    optionText="name"
                                                    optionValue="id"
                                                    helperText="选择工单所属分类"
                                                />
                                            </ReferenceInput>
                                        </Box>
                                    </Box>
                                </CardContent>
                            </Card>

                        </Box>
                    </FormTab>

                    {/* 客户信息 */}
                    <FormTab label="客户信息" path="customer">
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
                                <CardHeader
                                    title="客户详情"
                                    slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                                    sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
                                />
                                <CardContent>
                                    <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                        <Box sx={{ flex: 1, minWidth: '250px' }}>
                                            <TextInput
                                                source="customer_name"
                                                label="客户姓名"
                                                fullWidth
                                            />
                                        </Box>
                                        <Box sx={{ flex: 1, minWidth: '250px' }}>
                                            <TextInput
                                                source="customer_email"
                                                label="客户邮箱"
                                                type="email"
                                                fullWidth
                                            />
                                        </Box>
                                    </Box>

                                </CardContent>
                            </Card>

                            <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
                                <CardHeader
                                    title="联系信息"
                                    slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                                    sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
                                />
                                <CardContent>
                                    <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                        <Box sx={{ flex: 1, minWidth: '200px' }}>
                                            <TextInput
                                                source="customer_phone"
                                                label="客户电话"
                                                fullWidth
                                            />
                                        </Box>
                                    </Box>
                                </CardContent>
                            </Card>
                        </Box>
                    </FormTab>

                    {/* 时间与SLA */}
                    <FormTab label="时间与SLA" path="timing">
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
                                <CardHeader
                                    title="时间管理"
                                    slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                                    sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
                                />
                                <CardContent>
                                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                        <DateTimeInput
                                            source="due_date"
                                            label="截止时间"
                                            fullWidth
                                        />

                                    </Box>
                                </CardContent>
                            </Card>
                        </Box>
                    </FormTab>

                    {/* 附加信息 */}
                    <FormTab label="附加信息" path="additional">
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
                                <CardHeader
                                    title="工单设置"
                                    slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                                    sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
                                />
                                <CardContent>
                                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                        <TextInput
                                            source="tags"
                                            label="标签"
                                            fullWidth
                                            helperText="用逗号分隔多个标签"
                                            format={formatTagsInputValue}
                                        />

                                        {!requester && (
                                            <TextInput
                                                source="internal_notes"
                                                label="内部备注"
                                                fullWidth
                                                multiline
                                                rows={4}
                                            />
                                        )}
                                        <TextInput
                                            source="custom_fields"
                                            label="扩展字段（JSON）"
                                            fullWidth
                                            multiline
                                            rows={8}
                                            format={formatCustomFieldsInputValue}
                                            validate={validateCustomFieldsInput}
                                            helperText='请输入 JSON 对象，例如 {"asset_id":"A-1001"}'
                                        />
                                    </Box>
                                </CardContent>
                            </Card>
                        </Box>
                    </FormTab>
                    </TabbedForm>
                </TicketEditAuthorization>
            </Edit>
        </Box>
    );
};

export default TicketEdit;

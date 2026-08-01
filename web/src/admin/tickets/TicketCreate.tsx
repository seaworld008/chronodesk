import React from 'react';
import {
    Create,
    TextInput,
    SelectInput,
    NumberInput,
    BooleanInput,
    DateTimeInput,
    ReferenceInput,
    required,
    TopToolbar,
    ListButton,
    SaveButton,
    TabbedForm,
    FormTab,
    FormDataConsumer,
} from 'react-admin';
import {
    Box,
    Typography,
    Card,
    CardContent,
    CardHeader,
    Alert,
    AlertTitle,
    CircularProgress,
} from '@mui/material';
import { minCharacters, maxCharacters } from '@/lib/validators';
import {
    normalizeTagsForSubmit,
    normalizeCustomFieldsForSubmit,
} from './tagUtils';
import TagChipInput from './TagChipInput';
import BackButton from '../common/BackButton';
import { CreateTicketRequest } from '@/types';
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient';
import {
    humanApiRoutes,
    type IntakeRequestTypeVersion,
    type ProjectIntakeConfiguration,
} from '@/lib/generated/human-api';
import { resolveActiveProjectKey } from '@/lib/projectScope';
import { EnterpriseReferenceAutocompleteInput } from '@/components/inputs/EnterpriseFilterInputs';

type JSONSchemaProperty = {
    type?: string | string[];
    title?: string;
    description?: string;
    enum?: unknown[];
    minimum?: number;
    maximum?: number;
    maxLength?: number;
};

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

type TicketCreateFormValues = CreateTicketRequest & {
    tags?: unknown;
    custom_fields?: unknown;
    [key: string]: unknown;
};

const transformTicketCreate = (
    data: TicketCreateFormValues,
    intake: ProjectIntakeConfiguration,
): Record<string, unknown> => {
    const selectedRequestType = intake.request_types.find(
        ({ id }) => id === data.request_type_version_id,
    );
    if (!selectedRequestType) {
        throw new Error('请选择当前项目已发布的请求类型');
    }
    const workflowVersionID =
        data.workflow_version_id || intake.workflows[0]?.id;
    if (!workflowVersionID) {
        throw new Error('当前项目没有可用的已发布工作流');
    }

    const payload: Record<string, unknown> = {};
    for (const field of [
        'title',
        'description',
        'priority',
        'source',
        'request_type_version_id',
        'assigned_to_id',
        'category_id',
        'subcategory_id',
        'due_date',
        'customer_name',
        'customer_email',
        'customer_phone',
    ] as const) {
        if (typeof data[field] !== 'undefined') {
            payload[field] = data[field];
        }
    }
    payload.type = selectedRequestType.work_class;
    payload.workflow_version_id = workflowVersionID;

    const normalizedTags = normalizeTagsForSubmit(data.tags);

    if (typeof normalizedTags !== 'undefined') {
        payload.tags = normalizedTags;
    } else {
        delete payload.tags;
    }

    const normalizedCustomFields = normalizeCustomFieldsForSubmit(data.custom_fields);
    if (typeof normalizedCustomFields !== 'undefined') {
        payload.custom_fields = normalizedCustomFields;
    } else {
        delete payload.custom_fields;
    }

    return payload;
};

const jsonObject = (value: unknown): Record<string, unknown> => {
    if (typeof value === 'string') {
        try {
            const parsed: unknown = JSON.parse(value);
            return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
                ? parsed as Record<string, unknown>
                : {};
        } catch {
            return {};
        }
    }
    return value && typeof value === 'object' && !Array.isArray(value)
        ? value as Record<string, unknown>
        : {};
};

const coreSchemaFields = new Set([
    'title',
    'summary',
    'description',
    'priority',
    'work_class',
    'type',
    'source',
]);

const schemaFieldOrder = (
    schema: Record<string, unknown>,
    uiSchemaValue: unknown,
): string[] => {
    const properties = jsonObject(schema.properties);
    const uiSchema = jsonObject(uiSchemaValue);
    const elements = Array.isArray(uiSchema.elements) ? uiSchema.elements : [];
    const ordered = elements.flatMap((element) => {
        const scope = jsonObject(element).scope;
        if (typeof scope !== 'string' || !scope.startsWith('#/properties/')) {
            return [];
        }
        return [scope.slice('#/properties/'.length)];
    });
    return [
        ...ordered,
        ...Object.keys(properties).filter((key) => !ordered.includes(key)),
    ];
};

const RequestTypeCustomFields = ({
    requestType,
}: {
    requestType?: IntakeRequestTypeVersion;
}) => {
    if (!requestType) {
        return <Alert severity="warning">请选择请求类型后填写扩展字段。</Alert>;
    }
    const schema = jsonObject(requestType.json_schema);
    const properties = jsonObject(schema.properties);
    const requiredFields = new Set(
        Array.isArray(schema.required)
            ? schema.required.filter((value): value is string => typeof value === 'string')
            : [],
    );
    const fields = schemaFieldOrder(schema, requestType.ui_schema)
        .filter((key) => !coreSchemaFields.has(key))
        .map((key) => ({
            key,
            property: jsonObject(properties[key]) as JSONSchemaProperty,
        }));

    if (fields.length === 0) {
        return (
            <Alert severity="info">
                此请求类型没有额外字段，核心字段会按已发布 Schema 自动校验。
            </Alert>
        );
    }

    return (
        <>
            {fields.map(({ key, property }) => {
                const label = property.title?.trim() || key;
                const source = `custom_fields.${key}`;
                const isRequired = requiredFields.has(key);
                const validators = isRequired ? [required()] : undefined;
                const propertyType = Array.isArray(property.type)
                    ? property.type.find((value) => value !== 'null')
                    : property.type;

                if (Array.isArray(property.enum)) {
                    return (
                        <SelectInput
                            key={key}
                            source={source}
                            label={label}
                            choices={property.enum.map((value) => ({
                                id: value,
                                name: String(value),
                            }))}
                            validate={validators}
                            required={isRequired}
                            helperText={property.description}
                            fullWidth
                        />
                    );
                }
                if (propertyType === 'boolean') {
                    return (
                        <BooleanInput
                            key={key}
                            source={source}
                            label={label}
                            validate={validators}
                            helperText={property.description}
                        />
                    );
                }
                if (propertyType === 'integer' || propertyType === 'number') {
                    return (
                        <NumberInput
                            key={key}
                            source={source}
                            label={label}
                            min={property.minimum}
                            max={property.maximum}
                            validate={validators}
                            required={isRequired}
                            helperText={property.description}
                            fullWidth
                        />
                    );
                }
                const textValidators = [
                    ...(validators ?? []),
                    ...(typeof property.maxLength === 'number'
                        ? [maxCharacters(property.maxLength, `不能超过 ${property.maxLength} 个字符`)]
                        : []),
                ];
                return (
                    <TextInput
                        key={key}
                        source={source}
                        label={label}
                        validate={textValidators.length > 0 ? textValidators : undefined}
                        required={isRequired}
                        helperText={property.description}
                        multiline={typeof property.maxLength === 'number' && property.maxLength > 255}
                        rows={typeof property.maxLength === 'number' && property.maxLength > 255 ? 4 : undefined}
                        fullWidth
                    />
                );
            })}
        </>
    );
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
    const [intake, setIntake] =
        React.useState<ProjectIntakeConfiguration>();
    const [configurationError, setConfigurationError] = React.useState('');

    React.useEffect(() => {
        let active = true;
        void resolveActiveProjectKey()
            .then((projectKey) => apiFetch<ProjectIntakeConfiguration>(
                humanApiRoutes.getProjectIntakeConfiguration({ projectKey }),
            ))
            .then((configuration) => {
                if (!active) return;
                if (
                    !Array.isArray(configuration.request_types) ||
                    configuration.request_types.length === 0 ||
                    !Array.isArray(configuration.workflows) ||
                    configuration.workflows.length === 0
                ) {
                    throw new Error('当前项目尚未发布可用于建单的请求类型和工作流');
                }
                setIntake(configuration);
            })
            .catch((error: unknown) => {
                if (!active) return;
                setConfigurationError(
                    localizedUnknownErrorMessage(error, '加载项目建单配置失败'),
                );
            });
        return () => {
            active = false;
        };
    }, []);

    if (configurationError) {
        return (
            <Box sx={{ p: 3 }}>
                <BackButton />
                <Alert severity="error" sx={{ mt: 2 }}>
                    <AlertTitle>无法创建工单</AlertTitle>
                    {configurationError}
                </Alert>
            </Box>
        );
    }
    if (!intake) {
        return (
            <Box
                sx={{
                    minHeight: 320,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    gap: 2,
                }}
            >
                <CircularProgress size={24} />
                <Typography>正在加载当前项目的已发布建单配置…</Typography>
            </Box>
        );
    }

    const initialRequestType =
        intake.request_types.find(({ work_class: workClass }) => workClass === 'request') ??
        intake.request_types[0];
    const defaultValues: Partial<TicketCreateFormValues> = {
        priority: 'normal',
        source: 'web',
        type: initialRequestType.work_class,
        request_type_version_id: initialRequestType.id,
        workflow_version_id: intake.workflows[0].id,
    };

    return (
        <Box sx={{ p: 3 }}>
            <BackButton />
            <Alert severity="info" sx={{
                mb: 3,
                borderRadius: 2,
                backgroundColor: '#eff6ff',
                color: '#1e40af',
                border: '1px solid #dbeafe',
                '& .MuiAlert-icon': { color: '#2563eb' }
            }}>
                <AlertTitle sx={{ fontWeight: 600 }}>创建新工单</AlertTitle>
                <Typography variant="body2">
                    请填写工单的详细信息。工单创建后将自动分配给指定的负责人。
                </Typography>
            </Alert>

            <Create
                actions={<TicketCreateActions />}
                title="创建新工单"
                mutationMode="pessimistic"
                redirect="show"
                transform={(data) => transformTicketCreate(data, intake)}
            >
                <TabbedForm
                    toolbar={<TicketCreateToolbar />}
                    syncWithLocation={false}
                    defaultValues={defaultValues}
                >
                    {/* 基本信息 */}
                    <FormTab label="基本信息" path="">
                        <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
                            <CardHeader
                                title="工单基本信息"
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

                                    <ReferenceInput source="assigned_to_id" reference="assignees" label="分配给">
                                        <EnterpriseReferenceAutocompleteInput
                                            label="分配给"
                                            optionText={(choice) => `${choice.username} (${choice.first_name} ${choice.last_name})`}
                                            fullWidth
                                            helperText="输入姓名或用户名远程搜索负责处理此工单的人员"
                                        />
                                    </ReferenceInput>

                                    <TagChipInput
                                        source="tags"
                                        label="标签"
                                        fullWidth
                                        helperText="输入标签后按回车，便于分类和搜索"
                                    />
                                </Box>
                            </CardContent>
                        </Card>
                    </FormTab>

                    {/* 分类信息 */}
                    <FormTab label="分类与类型" path="category">
                        <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
                            <CardHeader
                                title="工单分类"
                                slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                                sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
                            />
                            <CardContent>
                                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                    <ReferenceInput source="category_id" reference="categories" label="工单类别" >
                                        <EnterpriseReferenceAutocompleteInput
                                            label="工单类别"
                                            optionText="name"
                                            optionValue="id"
                                            fullWidth
                                            helperText="选择工单所属的主要类别"
                                        />
                                    </ReferenceInput>

                                    <SelectInput
                                        source="request_type_version_id"
                                        label="请求类型"
                                        choices={intake.request_types.map((requestType) => ({
                                            id: requestType.id,
                                            name: requestType.name,
                                        }))}
                                        fullWidth
                                        required
                                        helperText={`来自当前项目配置发布 v${intake.release_version}`}
                                    />

                                </Box>
                            </CardContent>
                        </Card>
                    </FormTab>

                    {/* 时间与SLA */}
                    <FormTab label="时间管理" path="timeline">
                        <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
                            <CardHeader
                                title="时间与SLA设置"
                                slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                                sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
                            />
                            <CardContent>
                                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                    <DateTimeInput
                                        source="due_date"
                                        label="预期完成时间"
                                        fullWidth
                                        helperText="期望解决此工单的最晚时间"
                                    />

                                </Box>
                            </CardContent>
                        </Card>
                    </FormTab>

                    {/* 客户与扩展信息 */}
                    <FormTab label="客户与扩展" path="advanced">
                        <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
                            <CardHeader
                                title="客户与扩展信息"
                                slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                                sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
                            />
                            <CardContent>
                                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                    <TextInput
                                        source="customer_name"
                                        label="客户姓名"
                                        fullWidth
                                    />
                                    <TextInput
                                        source="customer_email"
                                        label="客户邮箱"
                                        type="email"
                                        fullWidth
                                    />
                                    <TextInput
                                        source="customer_phone"
                                        label="客户电话"
                                        fullWidth
                                    />
                                    <FormDataConsumer>
                                        {({ formData }) => (
                                            <RequestTypeCustomFields
                                                requestType={intake.request_types.find(
                                                    ({ id }) =>
                                                        id === formData.request_type_version_id,
                                                )}
                                            />
                                        )}
                                    </FormDataConsumer>
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

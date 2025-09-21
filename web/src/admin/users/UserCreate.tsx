import React from 'react';
import {
    Create,
    TextInput,
    SelectInput,
    BooleanInput,
    PasswordInput,
    required,
    email,
    minLength,
    maxLength,
    TopToolbar,
    ListButton,
    SaveButton,
    TabbedForm,
    FormTab,
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
import {
    Person as PersonIcon,
    Security as SecurityIcon,
    Settings as SettingsIcon,
    Info as InfoIcon,
} from '@mui/icons-material';
import BackButton from '../common/BackButton';

// 角色选项
const roleChoices = [
    { id: 'customer', name: '客户' },
    { id: 'agent', name: '客服代理' },
    { id: 'supervisor', name: '主管' },
    { id: 'admin', name: '管理员' },
];

// 状态选项
const statusChoices = [
    { id: 'active', name: '激活' },
    { id: 'inactive', name: '未激活' },
    { id: 'suspended', name: '暂停' },
];

// 时区选项
const timezoneChoices = [
    { id: 'Asia/Shanghai', name: '中国标准时间 (UTC+8)' },
    { id: 'Asia/Tokyo', name: '日本标准时间 (UTC+9)' },
    { id: 'Asia/Seoul', name: '韩国标准时间 (UTC+9)' },
    { id: 'Asia/Singapore', name: '新加坡时间 (UTC+8)' },
    { id: 'Asia/Hong_Kong', name: '香港时间 (UTC+8)' },
    { id: 'UTC', name: '协调世界时 (UTC+0)' },
    { id: 'America/New_York', name: '美国东部时间 (UTC-5)' },
    { id: 'America/Los_Angeles', name: '美国西部时间 (UTC-8)' },
    { id: 'Europe/London', name: '英国时间 (UTC+0)' },
    { id: 'Europe/Paris', name: '中欧时间 (UTC+1)' },
];

// 语言选项
const languageChoices = [
    { id: 'zh-CN', name: '中文简体' },
    { id: 'zh-TW', name: '中文繁体' },
    { id: 'en-US', name: 'English (US)' },
    { id: 'ja-JP', name: '日本語' },
    { id: 'ko-KR', name: '한국어' },
];

/**
 * 表单验证规则
 */
const validateUsername = [required('用户名不能为空'), minLength(3, '用户名至少3个字符'), maxLength(50, '用户名最多50个字符')];
const validateEmail = [required('邮箱不能为空'), email('请输入有效的邮箱地址')];
const validatePassword = [required('密码不能为空'), minLength(6, '密码至少6个字符')];
const validateName = [maxLength(50, '姓名最多50个字符')];
const validateDisplayName = [maxLength(100, '显示名称最多100个字符')];

const validatePhone = (value: string) => {
    if (value && !/^\+?[\d\s\-()]+$/.test(value)) {
        return '请输入有效的电话号码';
    }
};

const validatePasswordConfirm = (value: string, allValues?: Record<string, unknown>) => {
    const password = typeof allValues?.password === 'string' ? allValues.password : '';
    if (password && value !== password) {
        return '密码确认不匹配';
    }
};

/**
 * 创建指导信息
 */
const CreateGuidance: React.FC = () => (
    <Alert severity="info" icon={<InfoIcon />} sx={{ mb: 3 }}>
        <AlertTitle>创建用户指南</AlertTitle>
        <Typography variant="body2">
            创建新用户账户时，请确保填写准确的联系信息。新用户将收到包含初始密码的邮件通知。
            建议首次登录后要求用户修改密码以确保账户安全。
        </Typography>
    </Alert>
);

/**
 * 角色说明组件
 */
const RoleGuidance: React.FC = () => (
    <Alert severity="warning" icon={<SecurityIcon />} sx={{ mb: 2 }}>
        <AlertTitle>角色权限说明</AlertTitle>
        <Box component="ul" sx={{ mt: 1, mb: 0, pl: 2 }}>
            <Typography component="li" variant="body2">
                <strong>客户</strong>：只能查看和创建自己的工单
            </Typography>
            <Typography component="li" variant="body2">
                <strong>客服代理</strong>：可处理工单和客户咨询
            </Typography>
            <Typography component="li" variant="body2">
                <strong>主管</strong>：可管理团队和工单分配
            </Typography>
            <Typography component="li" variant="body2">
                <strong>管理员</strong>：拥有系统完全控制权限
            </Typography>
        </Box>
    </Alert>
);

/**
 * 安全提醒组件
 */
const SecurityReminder: React.FC = () => (
    <Alert severity="warning" sx={{ mb: 2 }}>
        <Typography variant="body2">
            <strong>安全提醒：</strong>
            创建的初始密码将通过邮件发送给用户。请确保邮箱地址准确无误，并提醒用户首次登录后立即修改密码。
        </Typography>
    </Alert>
);

/**
 * 顶部工具栏
 */
const UserCreateActions = () => (
    <TopToolbar>
        <ListButton />
    </TopToolbar>
);

/**
 * 自定义保存按钮工具栏
 */
const UserCreateToolbar = () => (
    <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', p: 2 }}>
        <SaveButton 
            label="创建用户"
            variant="contained"
            alwaysEnable
        />
        <Typography variant="body2" color="text.secondary">
            * 标记的字段为必填项
        </Typography>
    </Box>
);

/**
 * 表单默认值
 */
const defaultValues = {
    role: 'customer',
    status: 'active',
    timezone: 'Asia/Shanghai',
    language: 'zh-CN',
    email_verified: false,
};

/**
 * 用户创建页面
 */
const UserCreate: React.FC = () => {
    return (
        <Create 
            actions={<UserCreateActions />}
            title="创建新用户"
            mutationMode="pessimistic"
            redirect="show"
        >
            <Box sx={{ maxWidth: 1200, p: 3 }}>
                <BackButton fallbackPath="/users" />
                <CreateGuidance />
                
                <TabbedForm 
                    toolbar={<UserCreateToolbar />} 
                    syncWithLocation={false}
                    defaultValues={defaultValues}
                >
                    {/* 基本信息 */}
                    <FormTab label="基本信息" path="">
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <Card>
                                <CardHeader 
                                    title="账户信息" 
                                    avatar={<PersonIcon color="primary" />}
                                />
                                <CardContent>
                                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                        <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                            <Box sx={{ flex: 1, minWidth: '250px' }}>
                                                <TextInput
                                                    source="username"
                                                    label="用户名"
                                                    validate={validateUsername}
                                                    fullWidth
                                                    required
                                                    helperText="用于登录的唯一标识，建议使用英文字母和数字"
                                                />
                                            </Box>
                                            
                                            <Box sx={{ flex: 1, minWidth: '250px' }}>
                                                <TextInput
                                                    source="email"
                                                    label="邮箱地址"
                                                    type="email"
                                                    validate={validateEmail}
                                                    fullWidth
                                                    required
                                                    helperText="用于登录和接收系统通知，必须唯一"
                                                />
                                            </Box>
                                        </Box>
                                        
                                        <Box sx={{ flex: 1, minWidth: '250px' }}>
                                            <TextInput
                                                source="phone"
                                                label="电话号码"
                                                validate={validatePhone}
                                                fullWidth
                                                helperText="联系电话，支持国际格式"
                                            />
                                        </Box>
                                    </Box>
                                </CardContent>
                            </Card>
                        </Box>
                    </FormTab>
                    
                    {/* 个人信息 */}
                    <FormTab label="个人信息" path="personal">
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <Card>
                                <CardHeader title="个人详细信息" />
                                <CardContent>
                                    <Alert severity="info" sx={{ mb: 2 }}>
                                        <Typography variant="body2">
                                            个人信息可以在用户创建后由用户自行更新，这里填写的信息作为初始值。
                                        </Typography>
                                    </Alert>
                                    
                                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                        <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                            <Box sx={{ flex: 1, minWidth: '200px' }}>
                                                <TextInput
                                                    source="first_name"
                                                    label="名字"
                                                    validate={validateName}
                                                    fullWidth
                                                />
                                            </Box>
                                            
                                            <Box sx={{ flex: 1, minWidth: '200px' }}>
                                                <TextInput
                                                    source="last_name"
                                                    label="姓氏"
                                                    validate={validateName}
                                                    fullWidth
                                                />
                                            </Box>
                                            
                                            <Box sx={{ flex: 1, minWidth: '200px' }}>
                                                <TextInput
                                                    source="display_name"
                                                    label="显示名称"
                                                    validate={validateDisplayName}
                                                    fullWidth
                                                    helperText="在界面中显示的名称，如不填则使用姓名或用户名"
                                                />
                                            </Box>
                                        </Box>
                                        
                                        <TextInput
                                            source="avatar"
                                            label="头像URL"
                                            fullWidth
                                            helperText="头像图片的网络地址，用户后续可自行上传"
                                        />
                                    </Box>
                                </CardContent>
                            </Card>
                        </Box>
                    </FormTab>
                    
                    {/* 角色和权限 */}
                    <FormTab label="角色和权限" path="role">
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <RoleGuidance />
                            
                            <Card>
                                <CardHeader 
                                    title="角色设置" 
                                    avatar={<SecurityIcon color="primary" />}
                                />
                                <CardContent>
                                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                        <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                            <Box sx={{ flex: 1, minWidth: '250px' }}>
                                                <SelectInput
                                                    source="role"
                                                    label="用户角色"
                                                    choices={roleChoices}
                                                    required
                                                    helperText="选择用户在系统中的角色权限"
                                                />
                                            </Box>
                                            
                                            <Box sx={{ flex: 1, minWidth: '250px' }}>
                                                <SelectInput
                                                    source="status"
                                                    label="初始状态"
                                                    choices={statusChoices}
                                                    required
                                                    helperText="用户账户的初始状态，建议选择激活"
                                                />
                                            </Box>
                                        </Box>
                                        
                                        <Alert severity="info" sx={{ mt: 2 }}>
                                            <Typography variant="body2">
                                                <strong>建议：</strong>新创建的客户账户状态设为"未激活"，等待邮箱验证后自动激活。
                                                内部用户（代理、主管、管理员）可以直接设为"激活"状态。
                                            </Typography>
                                        </Alert>
                                    </Box>
                                </CardContent>
                            </Card>
                        </Box>
                    </FormTab>
                    
                    {/* 登录设置 */}
                    <FormTab label="登录设置" path="password">
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <SecurityReminder />
                            
                            <Card>
                                <CardHeader title="密码设置" />
                                <CardContent>
                                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                        <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                            <Box sx={{ flex: 1, minWidth: '250px' }}>
                                                <PasswordInput
                                                    source="password"
                                                    label="初始密码"
                                                    validate={validatePassword}
                                                    fullWidth
                                                    required
                                                    helperText="至少6个字符，包含字母和数字更安全"
                                                />
                                            </Box>
                                            
                                            <Box sx={{ flex: 1, minWidth: '250px' }}>
                                                <PasswordInput
                                                    source="password_confirm"
                                                    label="确认密码"
                                                    validate={validatePasswordConfirm}
                                                    fullWidth
                                                    required
                                                    helperText="请再次输入相同的密码"
                                                />
                                            </Box>
                                        </Box>
                                        
                                        <Alert severity="warning" sx={{ mt: 2 }}>
                                            <Typography variant="body2">
                                                创建用户后，系统将自动发送包含登录信息的邮件给用户。
                                                出于安全考虑，强烈建议用户首次登录后立即修改密码。
                                            </Typography>
                                        </Alert>
                                    </Box>
                                </CardContent>
                            </Card>
                        </Box>
                    </FormTab>
                    
                    {/* 偏好设置 */}
                    <FormTab label="偏好设置" path="preferences">
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <Card>
                                <CardHeader 
                                    title="系统偏好" 
                                    avatar={<SettingsIcon color="primary" />}
                                />
                                <CardContent>
                                    <Alert severity="info" sx={{ mb: 2 }}>
                                        <Typography variant="body2">
                                            这些设置决定了用户在系统中看到的默认时区和语言。
                                            用户登录后可以在个人设置中自行修改。
                                        </Typography>
                                    </Alert>
                                    
                                    <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                        <Box sx={{ flex: 1, minWidth: '250px' }}>
                                            <SelectInput
                                                source="timezone"
                                                label="默认时区"
                                                choices={timezoneChoices}
                                                helperText="影响系统中时间的显示格式"
                                            />
                                        </Box>
                                        
                                        <Box sx={{ flex: 1, minWidth: '250px' }}>
                                            <SelectInput
                                                source="language"
                                                label="界面语言"
                                                choices={languageChoices}
                                                helperText="用户界面显示语言"
                                            />
                                        </Box>
                                    </Box>
                                </CardContent>
                            </Card>
                        </Box>
                    </FormTab>
                    
                    {/* 验证设置 */}
                    <FormTab label="验证设置" path="verification">
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <Card>
                                <CardHeader title="邮箱验证" />
                                <CardContent>
                                    <Alert severity="info" sx={{ mb: 2 }}>
                                        <Typography variant="body2">
                                            邮箱验证设置决定了用户是否需要验证邮箱后才能正常使用系统功能。
                                        </Typography>
                                    </Alert>
                                    
                                    <BooleanInput
                                        source="email_verified"
                                        label="邮箱已验证"
                                        helperText="如果勾选，用户可以直接使用系统功能；如果不勾选，用户需要先验证邮箱"
                                    />
                                    
                                    <Alert severity="warning" sx={{ mt: 2 }}>
                                        <Typography variant="body2">
                                            <strong>建议：</strong>对于客户用户，建议不勾选此项，让他们通过邮件验证激活账户。
                                            对于内部用户，可以直接勾选以跳过邮箱验证步骤。
                                        </Typography>
                                    </Alert>
                                </CardContent>
                            </Card>
                        </Box>
                    </FormTab>
                </TabbedForm>
            </Box>
        </Create>
    );
};

export default UserCreate;

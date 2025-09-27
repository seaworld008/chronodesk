import React from 'react';
import {
    Edit,
    TextInput,
    SelectInput,
    BooleanInput,
    required,
    email,
    minLength,
    maxLength,
    TopToolbar,
    ListButton,
    ShowButton,
    DeleteButton,
    SaveButton,
    TabbedForm,
    FormTab,
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
    Avatar,
} from '@mui/material';
import {
    Person as PersonIcon,
    Security as SecurityIcon,
    Settings as SettingsIcon,
    Warning as WarningIcon,
} from '@mui/icons-material';
import BackButton from '../common/BackButton';

// 角色选项
const roleChoices = [
    { id: 'admin', name: '管理员' },
    { id: 'agent', name: '客服代理' },
    { id: 'customer', name: '客户' },
    { id: 'supervisor', name: '主管' },
];

// 状态选项
const statusChoices = [
    { id: 'active', name: '激活' },
    { id: 'inactive', name: '未激活' },
    { id: 'suspended', name: '暂停' },
    { id: 'deleted', name: '删除' },
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
const validateUsername = [required(), minLength(3), maxLength(50)];
const validateEmail = [required(), email()];
const validateName = [maxLength(50)];
const validateDisplayName = [maxLength(100)];
const validatePhone = (value: string) => {
    if (value && !/^\+?[\d\s\-()]+$/.test(value)) {
        return '请输入有效的电话号码';
    }
};

/**
 * 用户头像显示组件
 */
const UserAvatarDisplay: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const getInitials = (firstName: string, lastName: string, username: string) => {
        if (firstName && lastName) {
            return `${firstName[0]}${lastName[0]}`.toUpperCase();
        }
        if (username) {
            return username.substring(0, 2).toUpperCase();
        }
        return 'U';
    };

    const initials = getInitials(record.first_name, record.last_name, record.username);
    const fullName = `${record.first_name || ''} ${record.last_name || ''}`.trim();
    const displayName = record.display_name || fullName || record.username;

    return (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, mb: 2 }}>
            <Avatar
                src={record.avatar}
                sx={{
                    width: 60,
                    height: 60,
                    bgcolor: 'primary.main',
                    fontSize: '1.2rem',
                }}
            >
                {initials}
            </Avatar>
            <Box>
                <Typography variant="h6" fontWeight={600}>
                    {displayName}
                </Typography>
                <Typography variant="body2" color="text.secondary">
                    @{record.username} • ID: {record.id}
                </Typography>
            </Box>
        </Box>
    );
};

/**
 * 角色变更警告组件
 */
const RoleChangeWarning: React.FC = () => (
    <Alert severity="warning" icon={<SecurityIcon />} sx={{ mb: 3 }}>
        <AlertTitle>角色权限提醒</AlertTitle>
        <Typography variant="body2">
            修改用户角色将立即影响其系统访问权限：
        </Typography>
        <Box component="ul" sx={{ mt: 1, mb: 0, pl: 2 }}>
            <Typography component="li" variant="body2">
                <strong>管理员</strong>：拥有系统完全控制权限
            </Typography>
            <Typography component="li" variant="body2">
                <strong>主管</strong>：可管理团队和工单分配
            </Typography>
            <Typography component="li" variant="body2">
                <strong>客服代理</strong>：可处理工单和客户咨询
            </Typography>
            <Typography component="li" variant="body2">
                <strong>客户</strong>：只能查看和创建自己的工单
            </Typography>
        </Box>
    </Alert>
);

/**
 * 状态变更警告组件
 */
const StatusChangeWarning: React.FC = () => (
    <Alert severity="info" icon={<WarningIcon />} sx={{ mb: 2 }}>
        <Typography variant="body2">
            <strong>状态说明：</strong>
            激活 - 正常使用；未激活 - 需要验证；暂停 - 临时禁用；删除 - 永久禁用
        </Typography>
    </Alert>
);

/**
 * 顶部工具栏
 */
const UserEditActions = () => (
    <TopToolbar>
        <ListButton label="返回列表" />
        <ShowButton label="查看详情" />
        <DeleteButton label="删除" />
    </TopToolbar>
);

/**
 * 自定义保存按钮工具栏
 */
const UserEditToolbar = () => (
    <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', p: 2 }}>
        <SaveButton 
            label="保存更改"
            variant="contained"
        />
        <Typography variant="body2" color="text.secondary">
            * 标记的字段为必填项
        </Typography>
    </Box>
);

/**
 * 用户编辑页面
 */
const UserEdit: React.FC = () => {
    return (
        <Edit 
            actions={<UserEditActions />}
            title="编辑用户"
            mutationMode="pessimistic"
        >
            <Box sx={{ maxWidth: 1200, p: 3 }}>
                <BackButton fallbackPath="/users" />
                <TabbedForm toolbar={<UserEditToolbar />} syncWithLocation={false}>
                    {/* 基本信息 */}
                    <FormTab label="基本信息" path="">
                        <UserAvatarDisplay />
                        
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
                                                    helperText="用户名用于登录，建议使用英文字母和数字"
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
                                                    helperText="用于登录和接收系统通知"
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
                                                    helperText="在界面中显示的名称"
                                                />
                                            </Box>
                                        </Box>
                                        
                                        <TextInput
                                            source="avatar"
                                            label="头像URL"
                                            fullWidth
                                            helperText="头像图片的网络地址"
                                        />
                                    </Box>
                                </CardContent>
                            </Card>
                        </Box>
                    </FormTab>
                    
                    {/* 角色和权限 */}
                    <FormTab label="角色和权限" path="role">
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <RoleChangeWarning />
                            
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
                                                    label="账户状态"
                                                    choices={statusChoices}
                                                    required
                                                    helperText="控制用户账户的启用状态"
                                                />
                                            </Box>
                                        </Box>
                                        
                                        <StatusChangeWarning />
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
                                    <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                        <Box sx={{ flex: 1, minWidth: '250px' }}>
                                            <SelectInput
                                                source="timezone"
                                                label="时区设置"
                                                choices={timezoneChoices}
                                                helperText="用户所在的时区，影响时间显示"
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
                    
                    {/* 安全设置 */}
                    <FormTab label="安全设置" path="security">
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                            <Alert severity="warning" sx={{ mb: 3 }}>
                                <AlertTitle>安全提醒</AlertTitle>
                                <Typography variant="body2">
                                    修改安全设置可能影响用户登录状态。建议在用户同意的情况下进行操作。
                                </Typography>
                            </Alert>
                            
                            <Card>
                                <CardHeader title="验证和安全" />
                                <CardContent>
                                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                                        <Box sx={{ flex: 1, minWidth: '250px' }}>
                                            <BooleanInput
                                                source="email_verified"
                                                label="邮箱已验证"
                                                helperText="标记用户邮箱是否已通过验证"
                                            />
                                        </Box>
                                        
                                        <Alert severity="info" sx={{ mt: 2 }}>
                                            <Typography variant="body2">
                                                <strong>注意：</strong>密码修改需要通过专门的密码重置功能进行，此处不提供密码编辑选项。
                                            </Typography>
                                        </Alert>
                                    </Box>
                                </CardContent>
                            </Card>
                        </Box>
                    </FormTab>
                </TabbedForm>
            </Box>
        </Edit>
    );
};

export default UserEdit;

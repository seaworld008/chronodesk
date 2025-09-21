import React from 'react';
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
    Settings as SettingsIcon,
    Security as SecurityIcon,
} from '@mui/icons-material';

/**
 * 管理员系统配置页面
 */
const AdminSystemConfig: React.FC = () => {
    return (
        <Box sx={{ p: 3 }}>
            <Box sx={{ mb: 3 }}>
                <Typography variant="h4" gutterBottom sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                    <SettingsIcon />
                    系统配置
                </Typography>
                <Typography variant="body1" color="text.secondary">
                    管理系统的全局设置和配置参数
                </Typography>
            </Box>

            <Alert severity="info" sx={{ mb: 3 }}>
                <AlertTitle>系统配置说明</AlertTitle>
                <Typography variant="body2">
                    这些设置将影响整个系统的基础行为，请谨慎修改。
                </Typography>
            </Alert>

            <Card sx={{ mb: 3 }}>
                <CardHeader 
                    title="基本设置"
                    avatar={<SettingsIcon />}
                />
                <CardContent>
                    <Typography variant="body2">
                        系统基础配置功能正在开发中...
                    </Typography>
                </CardContent>
            </Card>

            <Card>
                <CardHeader 
                    title="安全配置"
                    avatar={<SecurityIcon />}
                />
                <CardContent>
                    <Typography variant="body2">
                        安全策略配置功能正在开发中...
                    </Typography>
                </CardContent>
            </Card>
        </Box>
    );
};

export default AdminSystemConfig;
import React from 'react';
import {
    Box,
    Card,
    CardContent,
    Typography,
    Alert,
} from '@mui/material';

/**
 * 安全设置组件
 */
const SecuritySettings: React.FC = () => {
    return (
        <Box>
            <Alert severity="info" sx={{ mb: 3 }}>
                安全设置功能正在开发中，敬请期待。
            </Alert>
            
            <Card>
                <CardContent>
                    <Typography variant="h6" gutterBottom>
                        安全设置
                    </Typography>
                    <Typography variant="body2" color="text.secondary">
                        这里将提供密码策略、登录限制、会话管理等安全相关配置。
                        包括密码强度要求、登录失败次数限制、会话超时设置等。
                    </Typography>
                </CardContent>
            </Card>
        </Box>
    );
};

export default SecuritySettings;
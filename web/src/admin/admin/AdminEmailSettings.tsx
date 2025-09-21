import React from 'react'
import { Title } from 'react-admin'
import { Box, Typography, Paper } from '@mui/material'

const AdminEmailSettings: React.FC = () => (
  <>
    <Title title="邮件设置" />
    <Box sx={{ p: 3 }}>
      <Paper sx={{ p: 3 }}>
        <Typography variant="h5" gutterBottom>
          邮件系统配置
        </Typography>
        <Typography color="text.secondary">
          该页面正在迁移到统一的系统设置中心，目前请使用“系统设置 → 邮件通知”页或后端配置脚本更新 SMTP 信息。
        </Typography>
      </Paper>
    </Box>
  </>
)

export default AdminEmailSettings

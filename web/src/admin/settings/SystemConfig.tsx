import React from 'react'
import { Title } from 'react-admin'
import { Box, Paper, Typography } from '@mui/material'

const SystemConfig: React.FC = () => (
  <>
    <Title title="系统配置" />
    <Box sx={{ p: 3 }}>
      <Paper sx={{ p: 3, maxWidth: 720 }}>
        <Typography variant="h5" gutterBottom>
          系统参数管理
        </Typography>
        <Typography color="text.secondary">
          详细的系统参数管理功能正在整合。当前请通过 <code>/api/admin/configs</code> API 或数据库中的 <code>system_configs</code> 表调整配置。
        </Typography>
      </Paper>
    </Box>
  </>
)

export default SystemConfig

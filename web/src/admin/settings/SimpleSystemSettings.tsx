import React from 'react'
import { Box, Paper, Typography } from '@mui/material'

const SimpleSystemSettings: React.FC = () => (
  <Box sx={{ p: 3 }}>
    <Paper sx={{ p: 3 }}>
      <Typography variant="h6" gutterBottom>
        基础设置
      </Typography>
      <Typography color="text.secondary">
        这里展示基础设置占位信息。详细配置请参考其它子页面或 API。
      </Typography>
    </Paper>
  </Box>
)

export default SimpleSystemSettings

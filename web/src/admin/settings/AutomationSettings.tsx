import React from 'react'
import { Box, Paper, Typography } from '@mui/material'

const AutomationSettings: React.FC = () => (
  <Box sx={{ p: 3 }}>
    <Paper sx={{ p: 3 }}>
      <Typography variant="h6" gutterBottom>
        自动化设置
      </Typography>
      <Typography color="text.secondary">
        自动化规则正在与 FE008 任务中的后端接口统一。请暂时通过后端 API `/api/admin/automation/rules` 管理规则。
      </Typography>
    </Paper>
  </Box>
)

export default AutomationSettings

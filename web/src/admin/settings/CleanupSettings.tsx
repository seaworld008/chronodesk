import React from 'react'
import { Box, Paper, Typography } from '@mui/material'

const CleanupSettings: React.FC = () => (
  <Box sx={{ p: 3 }}>
    <Paper sx={{ p: 3 }}>
      <Typography variant="h6" gutterBottom>
        数据清理策略
      </Typography>
      <Typography color="text.secondary">
        清理任务由后端调度器管理，可通过系统配置键 `cleanup.*` 调整。管理页面正在整合中。
      </Typography>
    </Paper>
  </Box>
)

export default CleanupSettings

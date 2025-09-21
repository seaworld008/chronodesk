import React from 'react'
import { Title } from 'react-admin'
import { Box, Paper, Typography } from '@mui/material'

const WorkingSystemSettings: React.FC = () => (
  <>
    <Title title="工作系统设置" />
    <Box sx={{ p: 3 }}>
      <Paper sx={{ p: 3 }}>
        <Typography variant="h5" gutterBottom>
          工作系统设置
        </Typography>
        <Typography color="text.secondary">
          该模块用于整合自动化、清理、通知等后台配置。目前请使用已有的 API 操作。
        </Typography>
      </Paper>
    </Box>
  </>
)

export default WorkingSystemSettings

import React from 'react'
import { Box, Grid, Paper, Typography } from '@mui/material'

const groups = [
  { title: '安全策略', description: '密码策略、登陆限制与会话管理。' },
  { title: '通知渠道', description: '邮件、Webhook、站内通知。' },
  { title: '清理策略', description: '日志与历史记录的保留周期。' },
  { title: '自动化', description: '工单自动化规则与 SLA 设置。' },
]

const SystemSettingsList: React.FC = () => (
  <Box sx={{ p: 3 }}>
    <Grid container spacing={3}>
      {groups.map((group) => (
        <Grid key={group.title} item xs={12} md={6}>
          <Paper sx={{ p: 3, height: '100%' }}>
            <Typography variant="h6" gutterBottom>
              {group.title}
            </Typography>
            <Typography color="text.secondary">{group.description}</Typography>
          </Paper>
        </Grid>
      ))}
    </Grid>
  </Box>
)

export default SystemSettingsList

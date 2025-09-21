import React from 'react'
import { Title } from 'react-admin'
import { Box, Grid, Paper, Typography, Button } from '@mui/material'
import { useNavigate } from 'react-router-dom'

const SimpleWorkingSystemSettings: React.FC = () => {
  const navigate = useNavigate()
  return (
    <>
      <Title title="系统设置" />
      <Box sx={{ p: 3 }}>
        <Typography variant="h4" gutterBottom>
          系统设置入口
        </Typography>
        <Typography color="text.secondary" paragraph>
          统一管理邮件、Webhook 以及系统基础配置。
        </Typography>
        <Grid container spacing={3}>
          <Grid item xs={12} md={6} lg={4}>
            <Paper sx={{ p: 3, height: '100%' }}>
              <Typography variant="h6" gutterBottom>
                邮件通知
              </Typography>
              <Typography color="text.secondary" paragraph>
                配置 SMTP 主机、模板与测试邮件。
              </Typography>
              <Button variant="contained" size="small" onClick={() => navigate('/email-settings')}>
                打开
              </Button>
            </Paper>
          </Grid>
          <Grid item xs={12} md={6} lg={4}>
            <Paper sx={{ p: 3, height: '100%' }}>
              <Typography variant="h6" gutterBottom>
                Webhook 集成
              </Typography>
              <Typography color="text.secondary" paragraph>
                管理企业即时通讯渠道的自动通知。
              </Typography>
              <Button variant="contained" size="small" onClick={() => navigate('/webhook-settings')}>
                打开
              </Button>
            </Paper>
          </Grid>
          <Grid item xs={12} md={6} lg={4}>
            <Paper sx={{ p: 3, height: '100%' }}>
              <Typography variant="h6" gutterBottom>
                系统概览
              </Typography>
              <Typography color="text.secondary" paragraph>
                查看并调整系统基础、安全、通知等配置。
              </Typography>
              <Button variant="contained" size="small" onClick={() => navigate('/system-settings/overview')}>
                打开
              </Button>
            </Paper>
          </Grid>
        </Grid>
      </Box>
    </>
  )
}

export default SimpleWorkingSystemSettings

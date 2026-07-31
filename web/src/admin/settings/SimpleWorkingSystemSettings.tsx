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
        <Typography
          sx={{
            color: "text.secondary",
            marginBottom: "16px"
          }}>
          统一管理平台邮件和全局基础配置。项目集成与智能体控制仅在具备相应项目职责时显示。
        </Typography>
        <Grid container spacing={3}>
          <Grid
            size={{
              xs: 12,
              md: 6,
              lg: 4
            }}>
            <Paper sx={{ p: 3, height: '100%' }}>
              <Typography variant="h6" gutterBottom>
                邮件通知
              </Typography>
              <Typography
                sx={{
                  color: "text.secondary",
                  marginBottom: "16px"
                }}>
                配置 SMTP 主机、模板与测试邮件。
              </Typography>
              <Button variant="contained" size="small" onClick={() => navigate('/email-settings')}>
                打开
              </Button>
            </Paper>
          </Grid>
          <Grid
            size={{
              xs: 12,
              md: 6,
              lg: 4
            }}>
            <Paper sx={{ p: 3, height: '100%' }}>
              <Typography variant="h6" gutterBottom>
                系统概览
              </Typography>
              <Typography
                sx={{
                  color: "text.secondary",
                  marginBottom: "16px"
                }}>
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
  );
}

export default SimpleWorkingSystemSettings

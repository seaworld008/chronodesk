import React from 'react'
import { Title } from 'react-admin'
import { Box, Paper, Typography, List, ListItem, ListItemText } from '@mui/material'

const providers = [
  { name: '企业微信', description: '通过官方机器人接口推送告警与工单动态。' },
  { name: '钉钉', description: '支持自定义关键词与加签的机器人通知。' },
  { name: '飞书', description: '使用 webhook 实现富文本卡片推送。' },
]

const WebhookIntegration: React.FC = () => (
  <>
    <Title title="Webhook 集成" />
    <Box sx={{ p: 3 }}>
      <Paper sx={{ p: 3, maxWidth: 720 }}>
        <Typography variant="h5" gutterBottom>
          即时通讯 Webhook
        </Typography>
        <Typography color="text.secondary" paragraph>
          Webhook 配置 API 已在后端实现（`/api/webhooks`）。图形化管理页面将在下一阶段回归，目前请通过 API 或数据库表 `webhook_configs` 维护。
        </Typography>
        <List>
          {providers.map((item) => (
            <ListItem key={item.name} disableGutters>
              <ListItemText primary={item.name} secondary={item.description} />
            </ListItem>
          ))}
        </List>
      </Paper>
    </Box>
  </>
)

export default WebhookIntegration

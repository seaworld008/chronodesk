import React from 'react'
import {
  Show,
  SimpleShowLayout,
  TextField,
  BooleanField,
  NumberField,
  DateField,
  FunctionField,
  TopToolbar,
  ListButton,
  EditButton,
} from 'react-admin'
import { Box, Typography } from '@mui/material'
import BackButton from '../common/BackButton'

const JsonDisplay: React.FC<{ value?: string }> = ({ value }) => {
  if (!value) {
    return <Box component="span">—</Box>
  }
  try {
    const parsed = JSON.parse(value)
    return (
      <Box
        sx={{
          fontFamily: 'monospace',
          fontSize: 13,
          backgroundColor: '#f5f5f5',
          borderRadius: 1,
          p: 1.5,
          whiteSpace: 'pre-wrap',
        }}
      >
        {JSON.stringify(parsed, null, 2)}
      </Box>
    )
  } catch (error) {
    return <Box component="span">{value}</Box>
  }
}

const ShowActions: React.FC = () => (
  <TopToolbar>
    <ListButton label="返回列表" />
    <EditButton label="编辑" />
  </TopToolbar>
)

const AutomationRuleShow: React.FC = (props) => (
  <Show {...props} actions={<ShowActions />}>
    <Box sx={{ p: 3 }}>
      <BackButton fallbackPath="/automation-rules" />
      <SimpleShowLayout>
      <TextField source="name" label="规则名称" />
      <TextField source="description" label="描述" />
      <TextField source="rule_type" label="规则类型" />
      <TextField source="trigger_event" label="触发事件" />
      <BooleanField source="is_active" label="启用" />
      <NumberField source="priority" label="优先级" />
      <FunctionField
        label="执行统计"
        render={(record) => `执行 ${record.execution_count ?? 0} 次，成功 ${record.success_count ?? 0}，失败 ${record.failure_count ?? 0}`}
      />
      <DateField source="last_executed_at" label="最近执行时间" showTime />
      <FunctionField
        label="条件"
        render={(record) => (
          <Box>
            <Typography variant="subtitle2" gutterBottom>
              条件
            </Typography>
            <JsonDisplay value={record?.conditions} />
          </Box>
        )}
      />
      <FunctionField
        label="动作"
        render={(record) => (
          <Box>
            <Typography variant="subtitle2" gutterBottom>
              动作
            </Typography>
            <JsonDisplay value={record?.actions} />
          </Box>
        )}
      />
      <TextField source="created_user.username" label="创建人" />
      <DateField source="created_at" label="创建时间" showTime />
      <DateField source="updated_at" label="更新时间" showTime />
    </SimpleShowLayout>
    </Box>
  </Show>
)

export default AutomationRuleShow

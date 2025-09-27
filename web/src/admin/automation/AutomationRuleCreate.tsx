import React from 'react'
import { Create, Toolbar, SaveButton, TopToolbar, ListButton } from 'react-admin'
import AutomationRuleForm, { buildTransform } from './AutomationRuleForm'

const CreateActions: React.FC = () => (
  <TopToolbar>
    <ListButton label="返回列表" />
  </TopToolbar>
)

const AutomationRuleCreate: React.FC = (props) => (
  <Create {...props} transform={buildTransform()} actions={<CreateActions />}>
    <AutomationRuleForm toolbar={<Toolbar><SaveButton /></Toolbar>} />
  </Create>
)

export default AutomationRuleCreate

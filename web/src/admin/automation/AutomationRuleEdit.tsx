import React from 'react'
import { Edit, Toolbar, SaveButton, DeleteButton, TopToolbar, ListButton, ShowButton } from 'react-admin'
import AutomationRuleForm, { buildTransform } from './AutomationRuleForm'

const RuleEditToolbar: React.FC = () => (
  <Toolbar>
    <SaveButton alwaysEnable />
    <DeleteButton redirect="list" mutationMode="pessimistic" />
  </Toolbar>
)

const RuleEditActions: React.FC = () => (
  <TopToolbar>
    <ListButton label="返回列表" />
    <ShowButton label="查看详情" />
  </TopToolbar>
)

const AutomationRuleEdit: React.FC = (props) => (
  <Edit {...props} mutationMode="pessimistic" transform={buildTransform()} actions={<RuleEditActions />}>
    <AutomationRuleForm toolbar={<RuleEditToolbar />} />
  </Edit>
)

export default AutomationRuleEdit

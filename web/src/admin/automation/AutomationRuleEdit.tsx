import React from 'react'
import { Edit, Toolbar, SaveButton, TopToolbar, ListButton, ShowButton } from 'react-admin'
import AutomationRuleForm, { buildTransform } from './AutomationRuleForm'
import { FocusSafeDeleteButton } from '@/components/actions/FocusSafeDeleteButtons'

const RuleEditToolbar: React.FC = () => (
  <Toolbar>
    <SaveButton />
    <FocusSafeDeleteButton redirect="list" mutationMode="pessimistic" />
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

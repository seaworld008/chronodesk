import type { ReactNode } from 'react'
import PageHeader from '@/components/layout/PageHeader'

interface AccountPageHeaderProps {
    title: string
    description: string
    action?: ReactNode
}

const AccountPageHeader = ({
    title,
    description,
    action,
}: AccountPageHeaderProps) => (
    <PageHeader
        title={title}
        description={description}
        action={action}
        testId="account-page-header"
    />
)

export default AccountPageHeader

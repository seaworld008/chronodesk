import assert from 'node:assert/strict'
import test, { after } from 'node:test'
import { createServer } from 'vite'

const vite = await createServer({
    appType: 'custom',
    server: { hmr: false, middlewareMode: true },
})

after(async () => {
    await vite.close()
})

test('导航图标目录为每个语义键绑定唯一 MUI Filled 图标', async () => {
    const registryModule = await vite.ssrLoadModule(
        '/src/navigation/navigationRegistry.ts',
    )
    const iconCatalog = await vite.ssrLoadModule(
        '/src/navigation/navigationIconCatalog.ts',
    )
    const expected = {
        workspaceHub: 'Workspaces',
        operationsDashboard: 'Analytics',
        crossProjectBoard: 'ViewKanban',
        projectOperations: 'SupportAgent',
        projectOverview: 'Home',
        ticketManagement: 'ConfirmationNumber',
        knowledgeBase: 'MenuBook',
        projectNotifications: 'Notifications',
        intelligentOperations: 'AutoAwesome',
        humanAgentCollaboration: 'Handshake',
        automationRules: 'AutoFixHigh',
        agentManagement: 'SmartToy',
        integrationCenter: 'Cable',
        webhookSettings: 'Webhook',
        integrationRuntime: 'Hub',
        projectSettings: 'Tune',
        projectInformation: 'Info',
        projectMembers: 'Groups',
        ticketIntake: 'DynamicForm',
        slaPolicies: 'Timer',
        intakeQueues: 'Inbox',
        ticketTemplates: 'Description',
        quickReplies: 'Quickreply',
        notificationDelivery: 'Send',
        governanceCenter: 'Policy',
        platformDashboard: 'SpaceDashboard',
        projectGovernance: 'AccountTree',
        identityAccess: 'ManageAccounts',
        auditCenter: 'FactCheck',
        emergencyControls: 'CrisisAlert',
        systemSettings: 'SettingsApplications',
        publicConfiguration: 'Settings',
        emailDelivery: 'OutgoingMail',
        accountHub: 'AccountCircle',
        accountProfile: 'Person',
        accountSecurity: 'Password',
        trustedDevices: 'Devices',
        loginHistory: 'History',
    }

    assert.deepEqual(iconCatalog.navigationIconNames, expected)
    assert.deepEqual(
        Object.keys(iconCatalog.navigationIconComponents).sort(),
        [...registryModule.navigationIconValues].sort(),
    )
    assert.equal(
        new Set(Object.values(iconCatalog.navigationIconNames)).size,
        registryModule.navigationIconValues.length,
        '不同语义键不能退化为同一个 MUI 图标',
    )
    assert.equal(
        new Set(Object.values(iconCatalog.navigationIconComponents)).size,
        registryModule.navigationIconValues.length,
        '图标目录不能让两个语义键指向同一个组件',
    )
})

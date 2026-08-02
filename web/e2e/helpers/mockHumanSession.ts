import type { Page, Route } from '@playwright/test';
import type {
    AuthorizedProject,
    AuthorizedProjectAccess,
    BusinessUnit,
    Organization,
    PlatformRole,
    ProjectRole,
} from '../../src/lib/generated/human-api';

export type MockSessionIdentity = {
    id: number;
    sessionID: string;
    email: string;
    platformRole: PlatformRole;
};

const timestamp = '2026-07-30T08:00:00Z';

const organization: Organization = {
    id: 1,
    public_id: '00000000-0000-7000-8000-000000000001',
    created_at: timestamp,
    updated_at: timestamp,
    slug: 'e2e-organization',
    name: 'E2E 组织',
    description: 'Playwright Human Web 契约组织',
    status: 'active',
};

const businessUnit: BusinessUnit = {
    id: 1,
    public_id: '00000000-0000-7000-8000-000000000002',
    created_at: timestamp,
    updated_at: timestamp,
    organization_id: organization.id,
    organization,
    key: 'E2E',
    name: 'E2E 业务单元',
    description: 'Playwright Human Web 契约业务单元',
    status: 'active',
};

const buildProject = (
    id: number,
    key: string,
    name: string,
): AuthorizedProject => ({
    id,
    public_id: `00000000-0000-7000-8000-${String(id).padStart(12, '0')}`,
    created_at: timestamp,
    updated_at: timestamp,
    organization_id: organization.id,
    organization,
    business_unit_id: businessUnit.id,
    business_unit: businessUnit,
    key,
    name,
    description: `${name} E2E 项目`,
    status: 'active',
    ticket_sequence: 9001,
});

export const projectA = buildProject(73, 'OPS', '运营支持');
export const projectB = buildProject(74, 'FIN', '财务服务');

export const defaultMockIdentity: MockSessionIdentity = {
    id: 42,
    sessionID: 'e2e-human-session',
    email: 'human-session@example.test',
    platformRole: 'member',
};

export const authorizedProjectAccess = (
    project: AuthorizedProject,
    projectRole: ProjectRole,
    canCreateKnowledgeDrafts =
        projectRole === 'project_admin' || projectRole === 'manager',
): AuthorizedProjectAccess => ({
    project,
    project_role: projectRole,
    can_create_knowledge_drafts: canCreateKnowledgeDrafts,
    scope: {
        organization_id: project.organization_id,
        project_id: project.id,
    },
});

const encodeBase64URL = (value: unknown): string =>
    btoa(JSON.stringify(value))
        .replace(/\+/g, '-')
        .replace(/\//g, '_')
        .replace(/=+$/u, '');

export const mockSessionToken = (
    identity: MockSessionIdentity,
    expiresAtSeconds = Math.floor(Date.now() / 1000) + 3600,
): string => [
    encodeBase64URL({ alg: 'none', typ: 'JWT' }),
    encodeBase64URL({
        sub: String(identity.id),
        sid: identity.sessionID,
        platform_role: identity.platformRole,
        exp: expiresAtSeconds,
    }),
    'e2e-signature',
].join('.');

export const installMockSession = async (
    page: Page,
    identity: MockSessionIdentity,
    activeProject?: AuthorizedProject,
    expiresAtSeconds = Math.floor(Date.now() / 1000) + 3600,
) => {
    const token = mockSessionToken(identity, expiresAtSeconds);
    await page.addInitScript(
        ({
            authToken,
            user,
            selectedProject,
            initializationKey,
            tokenExpiresAt,
        }) => {
            if (sessionStorage.getItem(initializationKey) === 'installed') {
                return;
            }
            sessionStorage.setItem(initializationKey, 'installed');
            localStorage.clear();
            localStorage.setItem('token', authToken);
            localStorage.setItem('refreshToken', `${user.sessionID}-refresh`);
            localStorage.setItem(
                'user',
                JSON.stringify({
                    id: user.id,
                    username: `e2e-${user.id}`,
                    email: user.email,
                    platform_role: user.platformRole,
                    status: 'active',
                    email_verified: true,
                    otp_enabled: false,
                }),
            );
            localStorage.setItem(
                'tokenExpiresAt',
                String(tokenExpiresAt),
            );
            if (selectedProject) {
                localStorage.setItem(
                    'chronodesk.activeProject',
                    JSON.stringify({
                        subject: String(user.id),
                        session_id: user.sessionID,
                        project_key: selectedProject.key,
                    }),
                );
            }
        },
        {
            authToken: token,
            user: identity,
            selectedProject: activeProject,
            initializationKey: `chronodesk.e2e.session.${identity.sessionID}`,
            tokenExpiresAt: expiresAtSeconds * 1000,
        },
    );
    return token;
};

export const fulfillJSON = (
    route: Route,
    body: unknown,
    status = 200,
    headers?: Record<string, string>,
) => route.fulfill({
    status,
    contentType: 'application/json',
    headers,
    body: JSON.stringify(body),
});

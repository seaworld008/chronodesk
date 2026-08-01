import {
  buildHumanApiRequest,
  type ArchivePlatformProjectOperationResponse,
  type CreatePlatformProjectOperationResponse,
  type HumanApiRequestOptions,
  type ListPlatformProjectsOperationResponse,
  type PlatformProjectSummary,
} from './generated/human-api'

const logoutWithoutBody: HumanApiRequestOptions<'deleteHumanSession'> = {
  pathParameters: {},
}
const logoutWithBody: HumanApiRequestOptions<'deleteHumanSession'> = {
  pathParameters: {},
  body: { refresh_token: 'opaque-refresh-token' },
}

buildHumanApiRequest('deleteHumanSession', logoutWithoutBody)
buildHumanApiRequest('deleteHumanSession', logoutWithBody)
buildHumanApiRequest('createHumanSession', {
  pathParameters: {},
  body: {
    email: 'human@example.com',
    password: 'not-a-real-secret',
  },
})

// @ts-expect-error Login has requestBody.required=true, so body is mandatory.
buildHumanApiRequest('createHumanSession', { pathParameters: {} })

buildHumanApiRequest('getHumanSessionUser', {
  pathParameters: {},
  // @ts-expect-error GET /auth/me has no request body.
  body: {},
})

buildHumanApiRequest('archivePlatformProject', {
  pathParameters: {
    projectPublicID: '019fb344-fa16-7e13-9c5b-08eb95478098',
  },
})

buildHumanApiRequest('archivePlatformProject', {
  // @ts-expect-error Project archive requires its public UUIDv7 path parameter.
  pathParameters: {},
})

buildHumanApiRequest('listPlatformProjects', {
  pathParameters: {},
})

const platformProjectSummary: PlatformProjectSummary = {
  public_id: '019fb344-fa16-7e13-9c5b-08eb95478098',
  created_at: '2026-07-31T00:00:00Z',
  updated_at: '2026-07-31T00:00:00Z',
  key: 'DEMO',
  name: '演示项目',
  description: '演示项目',
  status: 'active',
  business_unit: {
    public_id: '019fb344-fa16-7e13-9c5b-08eb95478099',
    key: 'OPS',
    name: '运营',
    description: '',
  },
}

const createdPlatformProject: CreatePlatformProjectOperationResponse = {
  code: 0,
  msg: '创建成功',
  data: platformProjectSummary,
}
void createdPlatformProject.data.public_id

const listedPlatformProjects: ListPlatformProjectsOperationResponse = {
  code: 0,
  msg: '获取成功',
  data: {
    items: [platformProjectSummary],
    total: 1,
    page: 1,
    page_size: 25,
    total_pages: 1,
  },
}
void listedPlatformProjects.data.items[0]?.public_id

const archivedPlatformProject: ArchivePlatformProjectOperationResponse = {
  code: 0,
  msg: '归档成功',
  data: {
    ...platformProjectSummary,
    status: 'archived',
  },
}
void archivedPlatformProject.data.public_id

// @ts-expect-error Successful project creation always includes data.
const createResponseWithoutData: CreatePlatformProjectOperationResponse = {
  code: 0,
  msg: '创建成功',
}
void createResponseWithoutData

// @ts-expect-error Successful project archival always includes data.
const archiveResponseWithoutData: ArchivePlatformProjectOperationResponse = {
  code: 0,
  msg: '归档成功',
}
void archiveResponseWithoutData

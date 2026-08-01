import type {
    CreateKnowledgeArticleRequest,
    CreateKnowledgeDraftRequest,
    KnowledgeArticle,
    KnowledgeArticlePage,
    KnowledgeAuthoredResult,
    KnowledgeCitation as GeneratedKnowledgeCitation,
    KnowledgeDocument,
    KnowledgeDocumentSection,
    KnowledgeSearchResult as GeneratedKnowledgeSearchResult,
    KnowledgeSource,
    KnowledgeVersion,
    KnowledgeVersionPage,
    VirusScanStatus,
} from '@/lib/generated/human-api'

export type {
    KnowledgeArticle,
    KnowledgeArticlePage,
    KnowledgeDocument,
    KnowledgeDocumentSection,
    KnowledgeSource,
    KnowledgeVersion,
    KnowledgeVersionPage,
}

export type KnowledgeCitation = GeneratedKnowledgeCitation
export type KnowledgeSearchResult = GeneratedKnowledgeSearchResult

export type KnowledgeArticleStatus = KnowledgeArticle['status']
export type KnowledgeVersionStatus = KnowledgeVersion['status']
export type KnowledgeVirusScanStatus = VirusScanStatus
export type CreateKnowledgeDraftInput = CreateKnowledgeArticleRequest
export type CreateKnowledgeVersionDraftInput = CreateKnowledgeDraftRequest
export type CreateKnowledgeDraftResult = KnowledgeAuthoredResult
export type KnowledgeTab = 'browse' | 'search' | 'mine' | 'manage'

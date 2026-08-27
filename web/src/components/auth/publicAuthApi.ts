import {
    humanApiRoutes,
    type ForgotPasswordRequest,
    type HumanRegistrationResult,
    type RegisterHumanRequest,
    type ResendHumanEmailVerificationRequest,
    type ResetHumanPasswordRequest,
    type VerifyHumanEmailRequest,
} from '@/lib/generated/human-api'
import { apiFetch } from '@/lib/apiClient'

export const registerHumanAccount = (
    request: RegisterHumanRequest,
): Promise<HumanRegistrationResult> =>
    apiFetch<HumanRegistrationResult>(humanApiRoutes.registerHuman(), {
        method: 'POST',
        credentials: 'include',
        body: JSON.stringify(request),
    })

export const requestHumanPasswordReset = (email: string): Promise<unknown> => {
    const request: ForgotPasswordRequest = { email }
    return apiFetch(humanApiRoutes.requestHumanPasswordReset(), {
        method: 'POST',
        body: JSON.stringify(request),
    })
}

export const resetHumanPassword = (
    request: ResetHumanPasswordRequest,
): Promise<unknown> =>
    apiFetch(humanApiRoutes.resetHumanPassword(), {
        method: 'POST',
        body: JSON.stringify(request),
    })

export const verifyHumanEmail = (
    request: VerifyHumanEmailRequest,
): Promise<unknown> =>
    apiFetch(humanApiRoutes.verifyHumanEmail(), {
        method: 'POST',
        body: JSON.stringify(request),
    })

export const resendHumanEmailVerification = (
    request: ResendHumanEmailVerificationRequest,
): Promise<unknown> =>
    apiFetch(humanApiRoutes.resendHumanEmailVerification(), {
        method: 'POST',
        body: JSON.stringify(request),
    })

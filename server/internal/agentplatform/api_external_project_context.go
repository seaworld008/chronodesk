package agentplatform

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/observability"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

// bindExternalProjectContext performs only the first short, live
// authorization transaction. Attachment handlers use this boundary because
// object-storage I/O and response streaming must happen without a database
// transaction; they open a second short transaction to revalidate and
// atomically finalize policy/domain state.
func (h *APIHandler) bindExternalProjectContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.projects == nil || h.native == nil || h.db == nil {
			WriteProblem(
				c,
				http.StatusServiceUnavailable,
				ProblemInternal,
				"Project authorization is unavailable",
				true,
			)
			c.Abort()
			return
		}
		principalID := strings.TrimSpace(
			c.GetString(agentauth.ContextPrincipalID),
		)
		credentialID := strings.TrimSpace(
			c.GetString(agentauth.ContextCredentialID),
		)
		projectKey := strings.TrimSpace(c.Param("projectKey"))
		tokenScopes, _ := c.Get(agentauth.ContextScopes)
		verifiedTokenScopes, ok := tokenScopes.([]string)
		if !ok {
			WriteProblem(
				c,
				http.StatusUnauthorized,
				ProblemUnauthorized,
				"Verified Agent scopes are unavailable",
				false,
			)
			c.Abort()
			return
		}
		verifiedTokenScopes = append(
			[]string(nil),
			verifiedTokenScopes...,
		)
		access, err := h.projects.ResolvePrincipalProject(
			c.Request.Context(),
			projectKey,
			principalID,
		)
		if err != nil {
			WriteProblem(
				c,
				http.StatusForbidden,
				ProblemPolicyDenied,
				"Project access is denied",
				false,
			)
			c.Abort()
			return
		}
		operationContext, err := services.WithOperationContext(
			c.Request.Context(),
			services.OperationContext{
				Scope:        access.Scope,
				Actor:        models.ServicePrincipalActor(principalID),
				Source:       services.SourceProtocolAgentREST,
				CredentialID: credentialID,
				TraceID: observability.TraceIDFromContext(
					c.Request.Context(),
				),
				CorrelationID: observability.CorrelationIDFromContext(
					c.Request.Context(),
				),
			},
		)
		if err != nil {
			WriteProblem(
				c,
				http.StatusForbidden,
				ProblemPolicyDenied,
				"Project operation context is invalid",
				false,
			)
			c.Abort()
			return
		}
		publications := &apiPublicationBuffer{
			projectKey: projectKey,
			seen:       make(map[uint]struct{}),
		}
		operationContext = context.WithValue(
			operationContext,
			apiPublicationBufferContextKey{},
			publications,
		)

		var currentAccess *services.ProjectAccess
		err = scopeddb.WithProjectScopeContextTransaction(
			operationContext,
			h.db,
			access.Scope,
			func(scopedContext context.Context) error {
				var revalidateErr error
				currentAccess, revalidateErr =
					h.native.RevalidatePrincipalProjectOperation(
						scopedContext,
						verifiedTokenScopes...,
					)
				if revalidateErr != nil {
					return revalidateErr
				}
				if currentAccess.Project.Key !=
					models.ProjectKey(projectKey) {
					return services.ErrProjectAccessDenied
				}
				return nil
			},
		)
		if err != nil || currentAccess == nil {
			if err == nil {
				err = services.ErrProjectAccessDenied
			}
			writeExternalAgentAuthorizationError(c, err)
			return
		}

		c.Set(
			agentauth.ContextScopes,
			intersectScopes(
				verifiedTokenScopes,
				currentAccess.Scopes,
			),
		)
		originalRequest := c.Request
		c.Request = originalRequest.WithContext(operationContext)
		c.Next()
		c.Request = originalRequest.WithContext(operationContext)

		for _, ticketID := range publications.ticketIDs {
			if h.publisher != nil {
				h.publisher.PublishTicket(projectKey, ticketID)
			}
		}
	}
}

func writeExternalAgentAuthorizationError(
	c *gin.Context,
	err error,
) {
	switch {
	case errors.Is(err, services.ErrProjectAccessDenied):
		WriteProblem(
			c,
			http.StatusForbidden,
			ProblemPolicyDenied,
			"Project access is denied",
			false,
		)
	case errors.Is(err, services.ErrInvalidCredential),
		errors.Is(err, services.ErrCredentialExpired),
		errors.Is(err, services.ErrPrincipalNotFound),
		errors.Is(err, services.ErrPrincipalDisabled),
		errors.Is(err, services.ErrPrincipalExpired):
		WriteProblem(
			c,
			http.StatusUnauthorized,
			ProblemUnauthorized,
			"Agent credential is no longer active",
			false,
		)
	default:
		WriteProblem(
			c,
			http.StatusInternalServerError,
			ProblemInternal,
			"Project authorization failed",
			true,
		)
	}
	c.Abort()
}

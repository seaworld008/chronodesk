package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const minimumOperationalBearerTokenBytes = 32

// OperationalEndpointAuth protects deployment-only surfaces such as
// Prometheus metrics. It stores only a digest of the configured bearer token,
// never accepts tokens from query parameters, and does not include credentials
// in error responses.
type OperationalEndpointAuth struct {
	bearerDigest                 [sha256.Size]byte
	hasBearer                    bool
	allowUnauthenticatedLoopback bool
}

func NewOperationalEndpointAuth(
	bearerToken string,
	allowUnauthenticatedLoopback bool,
) (*OperationalEndpointAuth, error) {
	token := strings.TrimSpace(bearerToken)
	if token != bearerToken {
		return nil, errors.New(
			"operational bearer token must not contain surrounding whitespace",
		)
	}
	if token == "" && !allowUnauthenticatedLoopback {
		return nil, errors.New(
			"operational endpoint requires a bearer token or loopback-only mode",
		)
	}
	if token != "" && len(token) < minimumOperationalBearerTokenBytes {
		return nil, errors.New(
			"operational bearer token must contain at least 32 bytes",
		)
	}
	auth := &OperationalEndpointAuth{
		allowUnauthenticatedLoopback: allowUnauthenticatedLoopback,
	}
	if token != "" {
		auth.bearerDigest = sha256.Sum256([]byte(token))
		auth.hasBearer = true
	}
	return auth, nil
}

func (auth *OperationalEndpointAuth) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if auth == nil {
			writeOperationalUnauthorized(c)
			return
		}
		if auth.hasBearer && auth.matchesBearer(c.GetHeader("Authorization")) {
			c.Next()
			return
		}
		if auth.allowUnauthenticatedLoopback &&
			isOperationalLoopback(c.ClientIP()) {
			c.Next()
			return
		}
		writeOperationalUnauthorized(c)
	}
}

func (auth *OperationalEndpointAuth) matchesBearer(header string) bool {
	if auth == nil || !auth.hasBearer {
		return false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) ||
		len(header) == len(prefix) ||
		strings.ContainsAny(header[len(prefix):], " \t\r\n") {
		return false
	}
	provided := sha256.Sum256([]byte(header[len(prefix):]))
	return subtle.ConstantTimeCompare(
		provided[:],
		auth.bearerDigest[:],
	) == 1
}

func isOperationalLoopback(clientIP string) bool {
	ip := net.ParseIP(strings.TrimSpace(clientIP))
	return ip != nil && ip.IsLoopback()
}

func writeOperationalUnauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", `Bearer realm="chronodesk-operations"`)
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"code":    "operational_auth_required",
		"message": "运维端点需要认证",
	})
}

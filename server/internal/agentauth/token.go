package agentauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	ContextPrincipalID   = "agent_principal_id"
	ContextPrincipalName = "agent_principal_name"
	ContextCredentialID  = "agent_credential_id"
	ContextScopes        = "agent_scopes"
	ContextActorType     = "actor_type"
)

var (
	ErrInvalidToken      = errors.New("invalid agent access token")
	ErrExpiredToken      = errors.New("agent access token expired")
	ErrInvalidAudience   = errors.New("invalid token audience")
	ErrInsufficientScope = errors.New("insufficient scope")
)

// Principal is the authorization snapshot used when issuing a token. It is
// deliberately independent of the persistence model.
type Principal struct {
	ID           string
	CredentialID string
	ClientID     string
	Name         string
	Scopes       []string
	Active       bool
	ExpiresAt    *time.Time
}

// CredentialStore authenticates a pre-registered service principal.
type CredentialStore interface {
	AuthenticateClient(ctx context.Context, clientID, clientSecret string) (*Principal, error)
	TouchCredential(ctx context.Context, credentialID string, usedAt time.Time) error
}

type AccessValidator interface {
	ValidateAccessContext(ctx context.Context, principalID, credentialID string) error
}

type Manager struct {
	secret          []byte
	issuer          string
	audience        string
	ttl             time.Duration
	now             func() time.Time
	accessValidator AccessValidator
	validationEvery time.Duration
}

func (m *Manager) SetAccessValidator(validator AccessValidator) {
	m.accessValidator = validator
}

type accessClaims struct {
	Iss          string `json:"iss"`
	Sub          string `json:"sub"`
	Aud          string `json:"aud"`
	Exp          int64  `json:"exp"`
	Iat          int64  `json:"iat"`
	JTI          string `json:"jti"`
	ClientID     string `json:"client_id"`
	PrincipalID  string `json:"principal_id"`
	CredentialID string `json:"credential_id"`
	Name         string `json:"name"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

type AccessContext struct {
	PrincipalID  string
	CredentialID string
	ClientID     string
	Name         string
	Scopes       []string
	JTI          string
	ExpiresAt    time.Time
}

func NewManager(secret, issuer, audience string, ttl time.Duration) *Manager {
	if ttl <= 0 || ttl > time.Hour {
		ttl = 15 * time.Minute
	}
	return &Manager{
		secret:          []byte(secret),
		issuer:          strings.TrimSpace(issuer),
		audience:        strings.TrimSpace(audience),
		ttl:             ttl,
		now:             time.Now,
		validationEvery: 5 * time.Second,
	}
}

func (m *Manager) Issue(principal *Principal, requestedScopes []string) (string, time.Time, error) {
	if principal == nil ||
		!principal.Active ||
		strings.TrimSpace(principal.ID) == "" ||
		strings.TrimSpace(principal.CredentialID) == "" ||
		strings.TrimSpace(principal.ClientID) == "" {
		return "", time.Time{}, ErrInvalidToken
	}
	now := m.now().UTC()
	if principal.ExpiresAt != nil && !principal.ExpiresAt.After(now) {
		return "", time.Time{}, ErrExpiredToken
	}

	scopes := normalizeScopes(requestedScopes)
	if len(scopes) == 0 {
		scopes = normalizeScopes(principal.Scopes)
	}
	if !containsAll(principal.Scopes, scopes) {
		return "", time.Time{}, ErrInsufficientScope
	}

	expiresAt := now.Add(m.ttl)
	if principal.ExpiresAt != nil && principal.ExpiresAt.Before(expiresAt) {
		expiresAt = principal.ExpiresAt.UTC()
	}
	claims := accessClaims{
		Iss:          m.issuer,
		Sub:          "service-principal:" + principal.ID,
		Aud:          m.audience,
		Exp:          expiresAt.Unix(),
		Iat:          now.Unix(),
		JTI:          randomID(),
		ClientID:     principal.ClientID,
		PrincipalID:  principal.ID,
		CredentialID: principal.CredentialID,
		Name:         principal.Name,
		Scope:        strings.Join(scopes, " "),
		TokenType:    "agent_access",
	}
	token, err := m.sign(claims)
	return token, expiresAt, err
}

func (m *Manager) Verify(token string) (*AccessContext, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}
	headerPayload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if err := json.Unmarshal(headerPayload, &header); err != nil ||
		header.Algorithm != "HS256" ||
		header.Type != "at+jwt" {
		return nil, ErrInvalidToken
	}

	unsigned := parts[0] + "." + parts[1]
	expected := m.signature(unsigned)
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(provided, expected) {
		return nil, ErrInvalidToken
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var claims accessClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrInvalidToken
	}

	now := m.now().UTC()
	if claims.Exp <= now.Unix() {
		return nil, ErrExpiredToken
	}
	if claims.Iss != m.issuer ||
		claims.TokenType != "agent_access" ||
		claims.PrincipalID == "" ||
		claims.CredentialID == "" ||
		claims.ClientID == "" ||
		claims.JTI == "" ||
		claims.Sub != "service-principal:"+claims.PrincipalID ||
		claims.Iat <= 0 ||
		claims.Iat > now.Add(30*time.Second).Unix() ||
		claims.Exp <= claims.Iat ||
		len(strings.Fields(claims.Scope)) == 0 {
		return nil, ErrInvalidToken
	}
	if claims.Aud != m.audience {
		return nil, ErrInvalidAudience
	}

	return &AccessContext{
		PrincipalID:  claims.PrincipalID,
		CredentialID: claims.CredentialID,
		ClientID:     claims.ClientID,
		Name:         claims.Name,
		Scopes:       normalizeScopes([]string{claims.Scope}),
		JTI:          claims.JTI,
		ExpiresAt:    time.Unix(claims.Exp, 0).UTC(),
	}, nil
}

func (m *Manager) Middleware(requiredScopes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			m.setBearerChallenge(c, "", requiredScopes)
			writeOAuthProblem(c, 401, "unauthorized", "Bearer access token is required")
			c.Abort()
			return
		}
		access, err := m.Verify(strings.TrimSpace(parts[1]))
		if err != nil {
			m.setBearerChallenge(c, "invalid_token", requiredScopes)
			writeOAuthProblem(c, 401, "unauthorized", "Access token is invalid or expired")
			c.Abort()
			return
		}
		if m.accessValidator != nil {
			if err := m.accessValidator.ValidateAccessContext(
				c.Request.Context(),
				access.PrincipalID,
				access.CredentialID,
			); err != nil {
				m.setBearerChallenge(c, "invalid_token", requiredScopes)
				writeOAuthProblem(c, 401, "unauthorized", "Access token credential is no longer active")
				c.Abort()
				return
			}
		}
		if !containsAll(access.Scopes, requiredScopes) {
			m.setBearerChallenge(c, "insufficient_scope", requiredScopes)
			writeOAuthProblem(c, 403, "insufficient_scope", "The access token does not grant the required scope")
			c.Abort()
			return
		}

		c.Set(ContextPrincipalID, access.PrincipalID)
		c.Set(ContextPrincipalName, access.Name)
		c.Set(ContextCredentialID, access.CredentialID)
		c.Set(ContextScopes, access.Scopes)
		c.Set(ContextActorType, "service_principal")
		requestContext, cancel := context.WithDeadline(c.Request.Context(), access.ExpiresAt)
		c.Request = c.Request.WithContext(requestContext)
		done := make(chan struct{})
		if m.accessValidator != nil {
			go func() {
				validationEvery := m.validationEvery
				if validationEvery <= 0 {
					validationEvery = 5 * time.Second
				}
				ticker := time.NewTicker(validationEvery)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						return
					case <-requestContext.Done():
						return
					case <-ticker.C:
						if err := m.accessValidator.ValidateAccessContext(
							requestContext,
							access.PrincipalID,
							access.CredentialID,
						); err != nil {
							cancel()
							return
						}
					}
				}
			}()
		}
		defer func() {
			close(done)
			cancel()
		}()
		c.Next()
	}
}

func (m *Manager) setBearerChallenge(c *gin.Context, errorCode string, requiredScopes []string) {
	parameters := []string{
		`realm="chronodesk-agent"`,
		`resource_metadata="` + quoteChallengeValue(protectedResourceMetadataURL(m.audience)) + `"`,
	}
	if errorCode != "" {
		parameters = append(parameters, `error="`+quoteChallengeValue(errorCode)+`"`)
	}
	if scopes := strings.Join(normalizeScopes(requiredScopes), " "); scopes != "" {
		parameters = append(parameters, `scope="`+quoteChallengeValue(scopes)+`"`)
	}
	c.Header("WWW-Authenticate", "Bearer "+strings.Join(parameters, ", "))
}

func protectedResourceMetadataURL(resource string) string {
	parsed, err := url.Parse(resource)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return ""
	}
	resourcePath := strings.TrimSuffix(parsed.EscapedPath(), "/")
	if resourcePath == "" {
		resourcePath = "/"
	}
	parsed.Path = "/.well-known/oauth-protected-resource"
	parsed.RawPath = ""
	if resourcePath != "/" {
		parsed.Path += resourcePath
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func quoteChallengeValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func HasScopes(c *gin.Context, scopes ...string) bool {
	value, exists := c.Get(ContextScopes)
	if !exists {
		return false
	}
	granted, ok := value.([]string)
	return ok && containsAll(granted, scopes)
}

func (m *Manager) sign(claims accessClaims) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "at+jwt"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(m.signature(unsigned)), nil
}

func (m *Manager) signature(unsigned string) []byte {
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(unsigned))
	return mac.Sum(nil)
}

func randomID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

func normalizeScopes(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes))
	result := make([]string, 0, len(scopes))
	for _, raw := range scopes {
		for _, scope := range strings.Fields(raw) {
			if scope == "" {
				continue
			}
			if _, exists := seen[scope]; exists {
				continue
			}
			seen[scope] = struct{}{}
			result = append(result, scope)
		}
	}
	return result
}

func containsAll(granted, required []string) bool {
	set := make(map[string]struct{}, len(granted))
	for _, scope := range normalizeScopes(granted) {
		set[scope] = struct{}{}
	}
	for _, scope := range normalizeScopes(required) {
		if _, ok := set[scope]; !ok {
			return false
		}
	}
	return true
}

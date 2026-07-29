package agentauth

import (
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/seaworld008/chronodesk/server/internal/agentcontract"
)

var SupportedScopes = agentcontract.SupportedScopes()

type Handler struct {
	store        CredentialStore
	baseURL      string
	resources    map[string]ProtectedResource
	ordered      []ProtectedResource
	tokenLimiter *anonymousLimiter
}

// ProtectedResource binds one exact RFC 8707 resource identifier to the
// audience-specific token manager that may issue and verify tokens for it.
type ProtectedResource struct {
	Name    string
	Manager *Manager
}

type HandlerOption func(*Handler)

func WithTokenRateLimit(limit int, window time.Duration) HandlerOption {
	return func(handler *Handler) {
		handler.tokenLimiter = newAnonymousLimiter(limit, window)
	}
}

func NewHandler(
	store CredentialStore,
	baseURL string,
	resources []ProtectedResource,
	options ...HandlerOption,
) *Handler {
	handler := &Handler{
		store:        store,
		baseURL:      strings.TrimSpace(baseURL),
		resources:    make(map[string]ProtectedResource, len(resources)),
		ordered:      make([]ProtectedResource, 0, len(resources)),
		tokenLimiter: newAnonymousLimiter(30, time.Minute),
	}
	for _, resource := range resources {
		if resource.Manager == nil {
			handler.ordered = append(handler.ordered, resource)
			continue
		}
		identifier := strings.TrimSpace(resource.Manager.audience)
		handler.resources[identifier] = resource
		handler.ordered = append(handler.ordered, resource)
	}
	for _, option := range options {
		option(handler)
	}
	return handler
}

func (h *Handler) RegisterPublicRoutes(router *gin.Engine) {
	router.POST("/oauth/token", h.limitTokenRequests(), h.Token)
	for _, resource := range h.ordered {
		if resource.Manager == nil {
			continue
		}
		metadataURL := protectedResourceMetadataURL(resource.Manager.audience)
		parsed, err := url.Parse(metadataURL)
		if err != nil || parsed.Path == "" {
			continue
		}
		router.GET(parsed.Path, h.protectedResourceMetadata(resource))
	}
	router.GET("/.well-known/oauth-authorization-server", h.AuthorizationServerMetadata)
}

func (h *Handler) Token(c *gin.Context) {
	if h.store == nil ||
		!validIssuer(h.baseURL) ||
		len(h.resources) == 0 ||
		!h.resourcesAreValid() {
		writeOAuthError(c, http.StatusServiceUnavailable, "temporarily_unavailable", "Agent authorization is not available")
		return
	}

	request, err := parseTokenRequest(c)
	if err != nil {
		if errors.Is(err, errInvalidClientAuthentication) {
			writeInvalidClient(c)
			return
		}
		writeOAuthError(c, http.StatusBadRequest, "invalid_request", "The token request is malformed")
		return
	}
	if request.grantType == "" {
		writeOAuthError(c, http.StatusBadRequest, "invalid_request", "The grant_type parameter is required")
		return
	}
	if request.grantType != "client_credentials" {
		writeOAuthError(c, http.StatusBadRequest, "unsupported_grant_type", "Only client_credentials is supported")
		return
	}
	if len(request.resources) != 1 {
		writeOAuthError(c, http.StatusBadRequest, "invalid_target", "The resource parameter must exactly match one advertised protected resource")
		return
	}
	resource, supported := h.resources[request.resources[0]]
	if !supported || resource.Manager == nil {
		writeOAuthError(c, http.StatusBadRequest, "invalid_target", "The resource parameter must exactly match one advertised protected resource")
		return
	}
	if request.clientID == "" || request.clientSecret == "" {
		writeInvalidClient(c)
		return
	}

	principal, err := h.store.AuthenticateClient(c.Request.Context(), request.clientID, request.clientSecret)
	if err != nil || principal == nil || !principal.Active {
		writeInvalidClient(c)
		return
	}

	effectiveScopes := strings.Fields(request.scope)
	if len(effectiveScopes) == 0 {
		effectiveScopes = principal.Scopes
	}
	token, expiresAt, err := resource.Manager.Issue(principal, effectiveScopes)
	if err != nil {
		if err == ErrInsufficientScope {
			writeOAuthError(c, http.StatusBadRequest, "invalid_scope", "Requested scope is not permitted")
			return
		}
		writeInvalidClient(c)
		return
	}
	_ = h.store.TouchCredential(c.Request.Context(), principal.CredentialID, time.Now().UTC())

	setNoStoreHeaders(c)
	expiresIn := int(expiresAt.Sub(resource.Manager.now().UTC()).Seconds())
	if expiresIn < 1 {
		expiresIn = 1
	}
	c.JSON(http.StatusOK, gin.H{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   expiresIn,
		"scope":        strings.Join(normalizeScopes(effectiveScopes), " "),
		"resource":     resource.Manager.audience,
	})
}

func (h *Handler) protectedResourceMetadata(resource ProtectedResource) gin.HandlerFunc {
	return func(c *gin.Context) {
		setMetadataCacheHeaders(c)
		c.JSON(http.StatusOK, gin.H{
			"resource":              resource.Manager.audience,
			"authorization_servers": []string{h.baseURL},
			"bearer_methods_supported": []string{
				"header",
			},
			"scopes_supported": SupportedScopes,
			"resource_name":    resource.Name,
		})
	}
}

func (h *Handler) AuthorizationServerMetadata(c *gin.Context) {
	setMetadataCacheHeaders(c)
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                         h.baseURL,
		"token_endpoint":                                 endpointURL(h.baseURL, "/oauth/token"),
		"grant_types_supported":                          []string{"client_credentials"},
		"token_endpoint_auth_methods_supported":          []string{"client_secret_basic", "client_secret_post"},
		"scopes_supported":                               SupportedScopes,
		"response_types_supported":                       []string{},
		"client_id_metadata_document_supported":          false,
		"authorization_response_iss_parameter_supported": false,
	})
}

func (h *Handler) resourcesAreValid() bool {
	seenMetadataPaths := make(map[string]struct{}, len(h.ordered))
	for _, resource := range h.ordered {
		manager := resource.Manager
		if manager == nil ||
			strings.TrimSpace(resource.Name) == "" ||
			manager.issuer != h.baseURL ||
			!validResourceIdentifier(manager.audience) {
			return false
		}
		metadataURL := protectedResourceMetadataURL(manager.audience)
		parsed, err := url.Parse(metadataURL)
		if err != nil || parsed.Path == "" {
			return false
		}
		if _, duplicate := seenMetadataPaths[parsed.Path]; duplicate {
			return false
		}
		seenMetadataPaths[parsed.Path] = struct{}{}
		if configured, exists := h.resources[manager.audience]; !exists || configured.Manager != manager {
			return false
		}
	}
	return len(seenMetadataPaths) == len(h.resources)
}

var (
	errMalformedTokenRequest       = errors.New("malformed token request")
	errInvalidClientAuthentication = errors.New("invalid client authentication")
)

type tokenRequest struct {
	grantType    string
	clientID     string
	clientSecret string
	scope        string
	resources    []string
}

func parseTokenRequest(c *gin.Context) (*tokenRequest, error) {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return nil, errMalformedTokenRequest
	}
	if err := c.Request.ParseForm(); err != nil {
		return nil, errMalformedTokenRequest
	}
	for _, name := range []string{"grant_type", "client_id", "client_secret", "scope"} {
		if len(c.Request.PostForm[name]) > 1 {
			return nil, errMalformedTokenRequest
		}
	}

	request := &tokenRequest{
		grantType:    c.Request.PostForm.Get("grant_type"),
		clientID:     c.Request.PostForm.Get("client_id"),
		clientSecret: c.Request.PostForm.Get("client_secret"),
		scope:        c.Request.PostForm.Get("scope"),
		resources:    append([]string(nil), c.Request.PostForm["resource"]...),
	}

	authorization := strings.TrimSpace(c.GetHeader("Authorization"))
	if authorization != "" {
		if request.clientID != "" || request.clientSecret != "" {
			return nil, errMalformedTokenRequest
		}
		username, password, ok := c.Request.BasicAuth()
		if !ok {
			return nil, errInvalidClientAuthentication
		}
		request.clientID, err = url.QueryUnescape(username)
		if err != nil {
			return nil, errInvalidClientAuthentication
		}
		request.clientSecret, err = url.QueryUnescape(password)
		if err != nil {
			return nil, errInvalidClientAuthentication
		}
	}

	return request, nil
}

func validIssuer(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil &&
		parsed.IsAbs() &&
		parsed.Host != "" &&
		parsed.User == nil &&
		parsed.RawQuery == "" &&
		parsed.Fragment == "" &&
		(parsed.Scheme == "https" || (parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())))
}

func validResourceIdentifier(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil &&
		parsed.IsAbs() &&
		parsed.Host != "" &&
		parsed.User == nil &&
		parsed.RawQuery == "" &&
		parsed.Fragment == "" &&
		(parsed.Scheme == "https" || (parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())))
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func endpointURL(issuer, path string) string {
	return strings.TrimSuffix(issuer, "/") + path
}

func setMetadataCacheHeaders(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=3600")
	c.Header("X-Content-Type-Options", "nosniff")
}

func setNoStoreHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
}

func writeInvalidClient(c *gin.Context) {
	c.Header("WWW-Authenticate", `Basic realm="chronodesk-oauth"`)
	writeOAuthError(c, http.StatusUnauthorized, "invalid_client", "Client authentication failed")
}

func writeOAuthError(c *gin.Context, status int, code, description string) {
	setNoStoreHeaders(c)
	c.JSON(status, gin.H{
		"error":             code,
		"error_description": description,
	})
}

func writeOAuthProblem(c *gin.Context, status int, code, detail string) {
	setNoStoreHeaders(c)
	requestID := c.GetString("request_id")
	if requestID == "" {
		requestID = c.GetHeader("X-Request-ID")
	}
	c.Header("Content-Type", "application/problem+json")
	c.JSON(status, gin.H{
		"type":       "https://chronodesk.local/problems/" + code,
		"title":      strings.ReplaceAll(code, "_", " "),
		"status":     status,
		"detail":     detail,
		"code":       code,
		"request_id": requestID,
		"retryable":  status >= 500,
	})
}

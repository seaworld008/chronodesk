package a2a

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/seaworld008/chronodesk/server/internal/agentcontract"
	"github.com/seaworld008/chronodesk/server/internal/version"
)

type AgentCard struct {
	Name                 string                    `json:"name"`
	Description          string                    `json:"description"`
	SupportedInterfaces  []AgentInterface          `json:"supportedInterfaces"`
	Provider             *AgentProvider            `json:"provider,omitempty"`
	Version              string                    `json:"version"`
	DocumentationURL     string                    `json:"documentationUrl,omitempty"`
	Capabilities         AgentCapabilities         `json:"capabilities"`
	SecuritySchemes      map[string]SecurityScheme `json:"securitySchemes,omitempty"`
	SecurityRequirements []SecurityRequirement     `json:"securityRequirements,omitempty"`
	DefaultInputModes    []string                  `json:"defaultInputModes"`
	DefaultOutputModes   []string                  `json:"defaultOutputModes"`
	Skills               []AgentSkill              `json:"skills"`
	IconURL              string                    `json:"iconUrl,omitempty"`
}

type AgentInterface struct {
	URL             string `json:"url"`
	ProtocolBinding string `json:"protocolBinding"`
	ProtocolVersion string `json:"protocolVersion"`
}

type AgentProvider struct {
	Organization string `json:"organization"`
	URL          string `json:"url"`
}

type AgentCapabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"pushNotifications"`
	ExtendedAgentCard bool `json:"extendedAgentCard,omitempty"`
}

type AgentSkill struct {
	ID                   string                `json:"id"`
	Name                 string                `json:"name"`
	Description          string                `json:"description"`
	Tags                 []string              `json:"tags"`
	Examples             []string              `json:"examples,omitempty"`
	InputModes           []string              `json:"inputModes,omitempty"`
	OutputModes          []string              `json:"outputModes,omitempty"`
	SecurityRequirements []SecurityRequirement `json:"securityRequirements,omitempty"`
}

type SecurityScheme struct {
	HTTPAuth *HTTPAuthSecurityScheme `json:"httpAuthSecurityScheme,omitempty"`
	OAuth2   *OAuth2SecurityScheme   `json:"oauth2SecurityScheme,omitempty"`
}

type HTTPAuthSecurityScheme struct {
	Description  string `json:"description,omitempty"`
	Scheme       string `json:"scheme"`
	BearerFormat string `json:"bearerFormat,omitempty"`
}

type OAuth2SecurityScheme struct {
	Description       string     `json:"description,omitempty"`
	Flows             OAuthFlows `json:"flows"`
	OAuth2MetadataURL string     `json:"oauth2MetadataUrl,omitempty"`
}

type OAuthFlows struct {
	ClientCredentials *ClientCredentialsOAuthFlow `json:"clientCredentials,omitempty"`
}

type ClientCredentialsOAuthFlow struct {
	TokenURL string            `json:"tokenUrl"`
	Scopes   map[string]string `json:"scopes"`
}

type SecurityRequirement struct {
	Schemes map[string]StringListValue `json:"schemes"`
}

type StringListValue struct {
	List []string `json:"list"`
}

type CardOptions struct {
	BaseURL          string
	ResourceURL      string
	AgentVersion     string
	OAuthMetadataURL string
	OAuthTokenURL    string
	ProviderName     string
	ProviderURL      string
	DocumentationURL string
}

var a2aScopes = agentcontract.ScopeDescriptions()

func DefaultAgentCard(opts CardOptions) AgentCard {
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:8081"
	}
	if opts.AgentVersion == "" {
		opts.AgentVersion = version.Version
	}
	if opts.ProviderName == "" {
		opts.ProviderName = "ChronoDesk"
	}
	if opts.ProviderURL == "" {
		opts.ProviderURL = baseURL
	}
	if opts.ResourceURL == "" {
		opts.ResourceURL = baseURL + "/a2a/v1"
	}
	if opts.OAuthMetadataURL == "" {
		opts.OAuthMetadataURL = baseURL + "/.well-known/oauth-authorization-server"
	}
	if opts.OAuthTokenURL == "" {
		opts.OAuthTokenURL = baseURL + "/oauth/token"
	}

	taskScope := StringListValue{List: []string{"tasks:manage", "tickets:read"}}
	return AgentCard{
		Name:        "ChronoDesk Ticket Agent",
		Description: "A2A server for ticket intake, query, work, comments, and escalation.",
		SupportedInterfaces: []AgentInterface{{
			URL:             opts.ResourceURL,
			ProtocolBinding: "JSONRPC",
			ProtocolVersion: ProtocolVersion,
		}},
		Provider: &AgentProvider{
			Organization: opts.ProviderName,
			URL:          opts.ProviderURL,
		},
		Version:          opts.AgentVersion,
		DocumentationURL: opts.DocumentationURL,
		Capabilities: AgentCapabilities{
			Streaming:         true,
			PushNotifications: true,
		},
		SecuritySchemes: map[string]SecurityScheme{
			"oauth2": {
				OAuth2: &OAuth2SecurityScheme{
					Description: "OAuth 2.0 client credentials with least-privilege scopes. " +
						"Token requests must include the exact RFC 8707 resource " + opts.ResourceURL + ".",
					OAuth2MetadataURL: opts.OAuthMetadataURL,
					Flows: OAuthFlows{ClientCredentials: &ClientCredentialsOAuthFlow{
						TokenURL: opts.OAuthTokenURL,
						Scopes:   cloneStringMap(a2aScopes),
					}},
				},
			},
			"bearer": {
				HTTPAuth: &HTTPAuthSecurityScheme{
					Description:  "Short-lived service-principal access token restricted to audience " + opts.ResourceURL + ".",
					Scheme:       "Bearer",
					BearerFormat: "JWT",
				},
			},
		},
		SecurityRequirements: []SecurityRequirement{
			{Schemes: map[string]StringListValue{"oauth2": taskScope}},
			{Schemes: map[string]StringListValue{"bearer": {List: []string{}}}},
		},
		DefaultInputModes:  []string{"text/plain", "application/json"},
		DefaultOutputModes: []string{"text/plain", "application/json"},
		Skills: []AgentSkill{
			{
				ID:          "ticket-intake",
				Name:        "Ticket Intake",
				Description: "Create a structured ticket using explicitly selected published request-type and workflow versions.",
				Tags:        []string{"tickets", "intake", "create"},
				Examples: []string{
					`{"skill":"ticket-intake","input":{"title":"API outage","description":"The public API is unavailable.","type":"incident","priority":"high","request_type_version_id":"018f0f95-9e85-7a2b-8c3d-1234567890ab","workflow_version_id":"018f0f95-9e85-7a2b-8c3d-1234567890ac"}}`,
				},
				InputModes:  []string{"application/json"},
				OutputModes: []string{"application/json"},
				SecurityRequirements: skillSecurityRequirements(
					"tasks:manage",
					"tickets:create",
				),
			},
			{
				ID:          "ticket-query",
				Name:        "Ticket Query",
				Description: "Retrieve an authorized ticket snapshot and its progress.",
				Tags:        []string{"tickets", "query", "status"},
				Examples:    []string{`{"skill":"ticket-query","input":{"ticket_id":42}}`},
				InputModes:  []string{"application/json"},
				OutputModes: []string{"application/json"},
				SecurityRequirements: skillSecurityRequirements(
					"tasks:manage",
					"tickets:read",
				),
			},
			{
				ID:          "ticket-work",
				Name:        "Ticket Work",
				Description: "Perform policy-authorized work against a linked ticket.",
				Tags:        []string{"tickets", "workflow", "automation"},
				Examples: []string{
					`{"skill":"ticket-work","input":{"operation":"claim","ticket_id":42,"expected_version":3,"lease_seconds":300}}`,
				},
				InputModes:  []string{"application/json"},
				OutputModes: []string{"text/plain", "application/json"},
				SecurityRequirements: skillSecurityRequirements(
					"tasks:manage",
					"tickets:read",
					"tickets:update",
					"tickets:assign",
					"tickets:transition",
				),
			},
			{
				ID:          "ticket-comment",
				Name:        "Ticket Comment",
				Description: "Add a public or internal comment to an authorized ticket.",
				Tags:        []string{"tickets", "comments", "collaboration"},
				Examples: []string{
					`{"skill":"ticket-comment","input":{"ticket_id":42,"expected_version":3,"lease_id":"lease-id","content":"Diagnostic evidence attached.","type":"internal"}}`,
				},
				InputModes:  []string{"application/json"},
				OutputModes: []string{"application/json"},
				SecurityRequirements: skillSecurityRequirements(
					"tasks:manage",
					"tickets:read",
					"comments:write",
				),
			},
			{
				ID:          "ticket-escalation",
				Name:        "Ticket Escalation",
				Description: "Escalate a ticket when policy and SLA conditions permit.",
				Tags:        []string{"tickets", "escalation", "sla"},
				Examples: []string{
					`{"skill":"ticket-escalation","input":{"ticket_id":42,"expected_version":3,"lease_id":"lease-id","reason":"SLA breached","priority":"critical"}}`,
				},
				InputModes:  []string{"application/json"},
				OutputModes: []string{"application/json"},
				SecurityRequirements: skillSecurityRequirements(
					"tasks:manage",
					"tickets:read",
					"tickets:transition",
				),
			},
		},
	}
}

func skillSecurityRequirements(scopes ...string) []SecurityRequirement {
	return []SecurityRequirement{
		{
			Schemes: map[string]StringListValue{
				"oauth2": {List: append([]string(nil), scopes...)},
			},
		},
		{
			Schemes: map[string]StringListValue{
				"bearer": {List: []string{}},
			},
		},
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

type cardDocument struct {
	body         []byte
	etag         string
	lastModified time.Time
}

func newCardDocument(card AgentCard) (cardDocument, error) {
	body, err := json.Marshal(card)
	if err != nil {
		return cardDocument{}, err
	}
	sum := sha256.Sum256(body)
	return cardDocument{
		body:         body,
		etag:         `"` + hex.EncodeToString(sum[:]) + `"`,
		lastModified: time.Now().UTC().Truncate(time.Second),
	}, nil
}

func (d cardDocument) Handler(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=300")
	c.Header("ETag", d.etag)
	c.Header("Last-Modified", d.lastModified.Format(http.TimeFormat))
	if matchETag(c.GetHeader("If-None-Match"), d.etag) {
		c.Status(http.StatusNotModified)
		return
	}
	if c.GetHeader("If-None-Match") == "" &&
		notModifiedSince(c.GetHeader("If-Modified-Since"), d.lastModified) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", d.body)
}

func matchETag(header, current string) bool {
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimSpace(candidate) == current || strings.TrimSpace(candidate) == "*" {
			return true
		}
	}
	return false
}

func notModifiedSince(header string, lastModified time.Time) bool {
	if strings.TrimSpace(header) == "" {
		return false
	}
	since, err := http.ParseTime(header)
	if err != nil {
		return false
	}
	return !lastModified.After(since)
}

package middleware

import "fmt"

// SecurityConfig contains response-header controls that are meaningful for
// current browsers. Obsolete X-XSS-Protection and cookie-CSRF compatibility
// code are intentionally absent: ChronoDesk authenticates explicit bearer
// credentials and does not use cookies as an ambient login authority.
type SecurityConfig struct {
	ContentTypeNosniff        bool
	XFrameOptions             string
	HSTSMaxAge                int
	HSTSIncludeSubdomains     bool
	HSTSPreload               bool
	ContentSecurityPolicy     string
	ReferrerPolicy            string
	PermissionsPolicy         string
	CrossOriginEmbedderPolicy string
	CrossOriginOpenerPolicy   string
	CrossOriginResourcePolicy string
	RemoveServerHeader        bool
	CustomHeaders             map[string]string
}

func DefaultSecurityConfig() *SecurityConfig {
	return &SecurityConfig{
		ContentTypeNosniff:        true,
		XFrameOptions:             "DENY",
		HSTSMaxAge:                31_536_000,
		HSTSIncludeSubdomains:     true,
		ContentSecurityPolicy:     "default-src 'self'",
		ReferrerPolicy:            "strict-origin-when-cross-origin",
		PermissionsPolicy:         "geolocation=(), microphone=(), camera=()",
		CrossOriginEmbedderPolicy: "require-corp",
		CrossOriginOpenerPolicy:   "same-origin",
		CrossOriginResourcePolicy: "same-origin",
		RemoveServerHeader:        true,
		CustomHeaders:             make(map[string]string),
	}
}

func DevelopmentSecurityConfig() *SecurityConfig {
	return &SecurityConfig{
		ContentTypeNosniff:        true,
		XFrameOptions:             "SAMEORIGIN",
		ContentSecurityPolicy:     "default-src 'self' 'unsafe-inline' 'unsafe-eval'",
		ReferrerPolicy:            "no-referrer-when-downgrade",
		CrossOriginResourcePolicy: "cross-origin",
		CustomHeaders:             make(map[string]string),
	}
}

func ProductionSecurityConfig() *SecurityConfig {
	return &SecurityConfig{
		ContentTypeNosniff:        true,
		XFrameOptions:             "DENY",
		HSTSMaxAge:                63_072_000,
		HSTSIncludeSubdomains:     true,
		HSTSPreload:               true,
		ContentSecurityPolicy:     "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'",
		ReferrerPolicy:            "strict-origin-when-cross-origin",
		PermissionsPolicy:         "geolocation=(), microphone=(), camera=(), payment=(), usb=(), magnetometer=(), gyroscope=()",
		CrossOriginEmbedderPolicy: "require-corp",
		CrossOriginOpenerPolicy:   "same-origin",
		CrossOriginResourcePolicy: "same-origin",
		RemoveServerHeader:        true,
		CustomHeaders:             make(map[string]string),
	}
}

func SecurityMiddleware(config *SecurityConfig) func(HTTPContext) {
	if config == nil {
		config = DefaultSecurityConfig()
	}
	return func(c HTTPContext) {
		if config.ContentTypeNosniff {
			setHeader(c, "X-Content-Type-Options", "nosniff")
		}
		setOptionalHeader(c, "X-Frame-Options", config.XFrameOptions)
		if config.HSTSMaxAge > 0 {
			value := fmt.Sprintf("max-age=%d", config.HSTSMaxAge)
			if config.HSTSIncludeSubdomains {
				value += "; includeSubDomains"
			}
			if config.HSTSPreload {
				value += "; preload"
			}
			setHeader(c, "Strict-Transport-Security", value)
		}
		setOptionalHeader(c, "Content-Security-Policy", config.ContentSecurityPolicy)
		setOptionalHeader(c, "Referrer-Policy", config.ReferrerPolicy)
		setOptionalHeader(c, "Permissions-Policy", config.PermissionsPolicy)
		setOptionalHeader(c, "Cross-Origin-Embedder-Policy", config.CrossOriginEmbedderPolicy)
		setOptionalHeader(c, "Cross-Origin-Opener-Policy", config.CrossOriginOpenerPolicy)
		setOptionalHeader(c, "Cross-Origin-Resource-Policy", config.CrossOriginResourcePolicy)
		if config.RemoveServerHeader {
			setHeader(c, "Server", "")
		}
		for key, value := range config.CustomHeaders {
			setHeader(c, key, value)
		}
		c.Next()
	}
}

func setOptionalHeader(c HTTPContext, key, value string) {
	if value != "" {
		setHeader(c, key, value)
	}
}

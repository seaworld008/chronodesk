package middleware

import (
	"os"
)

// HTTPContext is the small framework-neutral surface used by the active
// cross-cutting middleware. Gin-specific capabilities are accessed through
// GinHTTPContext inside the adapter helpers.
type HTTPContext interface {
	GetHeader(key string) string
	JSON(code int, obj interface{})
	Abort()
	Next()
	Set(key string, value interface{})
	Get(key string) (interface{}, bool)
}

// MiddlewareConfig contains only the cross-cutting HTTP middleware used by
// the Gin application. Human and service-principal authentication are
// installed explicitly by their authoritative auth modules.
type MiddlewareConfig struct {
	CORS       *CORSConfig
	RateLimit  *RateLimitConfig
	Logger     *LoggerConfig
	Recovery   *RecoveryConfig
	Security   *SecurityConfig
	Additional []func(HTTPContext)
}

// DefaultMiddlewareConfig returns safe development defaults for the active
// cross-cutting middleware stack.
func DefaultMiddlewareConfig() *MiddlewareConfig {
	return &MiddlewareConfig{
		// Rate limiting is deliberately not enabled by a process-local
		// default. Application composition must install the authoritative
		// distributed limiter for each authenticated or anonymous surface.
		CORS:     DefaultCORSConfig(),
		Logger:   DefaultLoggerConfig(),
		Recovery: DefaultRecoveryConfig(),
		Security: DefaultSecurityConfig(),
	}
}

func DevelopmentMiddlewareConfig() *MiddlewareConfig {
	config := DefaultMiddlewareConfig()
	config.CORS = DevelopmentCORSConfig()
	config.Security = DevelopmentSecurityConfig()
	config.Logger.Logger = NewSimpleLogger(os.Stdout, LogLevelDebug)
	return config
}

func ProductionMiddlewareConfig() *MiddlewareConfig {
	config := DefaultMiddlewareConfig()
	config.CORS = &CORSConfig{
		AllowOrigins:     []string{"https://yourdomain.com"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           43200,
	}
	config.Security = ProductionSecurityConfig()
	config.Logger.Logger = NewSimpleLogger(os.Stdout, LogLevelInfo)
	config.Recovery.DisablePrintStack = true
	return config
}

// SetupMiddlewares builds the active cross-cutting chain. Authentication is
// deliberately absent: each route group installs the human or machine auth
// middleware appropriate to its audience and scopes.
func SetupMiddlewares(config *MiddlewareConfig) []func(HTTPContext) {
	if config == nil {
		config = DefaultMiddlewareConfig()
	}

	middlewares := make([]func(HTTPContext), 0, 6)
	middlewares = append(middlewares, RequestIDMiddleware())
	if config.Recovery != nil {
		middlewares = append(middlewares, RecoveryMiddleware(config.Recovery))
	}
	if config.Logger != nil {
		middlewares = append(middlewares, LoggingMiddleware(config.Logger))
	}
	if config.Security != nil {
		middlewares = append(middlewares, SecurityMiddleware(config.Security))
	}
	if config.CORS != nil {
		middlewares = append(middlewares, CORS(config.CORS))
	}
	if config.RateLimit != nil {
		middlewares = append(middlewares, RateLimit(config.RateLimit))
	}
	middlewares = append(middlewares, config.Additional...)
	return middlewares
}

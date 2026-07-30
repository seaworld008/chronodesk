package asyncapi

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed asyncapi.yaml
var specification []byte

// Specification returns a defensive copy of the canonical event contract.
func Specification() []byte {
	result := make([]byte, len(specification))
	copy(result, specification)
	return result
}

// RegisterRoutes publishes the contract without authentication. It contains
// schemas and protocol semantics only, never deployment credentials.
func RegisterRoutes(router gin.IRoutes) {
	router.GET("/asyncapi.yaml", func(c *gin.Context) {
		c.Data(
			http.StatusOK,
			"application/vnd.aai.asyncapi+yaml;version=3.1.0",
			specification,
		)
	})
}

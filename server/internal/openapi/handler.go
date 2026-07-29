package openapi

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed openapi.yaml
var specification []byte

// Specification returns a copy of the canonical OpenAPI contract.
func Specification() []byte {
	result := make([]byte, len(specification))
	copy(result, specification)
	return result
}

// RegisterRoutes exposes the machine-readable API contract without requiring
// authentication. The document contains no credentials or deployment secrets.
func RegisterRoutes(router gin.IRoutes) {
	router.GET("/openapi.yaml", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/vnd.oai.openapi;version=3.2.0", specification)
	})
}

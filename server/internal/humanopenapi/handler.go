package humanopenapi

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed openapi.json
var document []byte

// Document returns an isolated copy of the canonical Human Web contract.
func Document() []byte {
	result := make([]byte, len(document))
	copy(result, document)
	return result
}

// RegisterRoutes publishes the contract without authentication. The document
// describes schemas and authorization boundaries but contains no credentials.
func RegisterRoutes(router gin.IRoutes) {
	router.GET("/human-openapi.json", func(c *gin.Context) {
		c.Data(
			http.StatusOK,
			"application/vnd.oai.openapi+json;version=3.2.0",
			document,
		)
	})
}

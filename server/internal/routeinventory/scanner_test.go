package routeinventory

import (
	"strings"
	"testing"
)

func TestInjectedGETRegistrationFailsClosedUntilClassified(t *testing.T) {
	source := []byte(`package app

type Router struct{}

func registerRoutes(router *Router) {
	router.GET("/new-directory", listNewDirectory)
}
`)
	registrations, err := scanSource(
		"internal/app/injected.go",
		source,
		true,
	)
	if err != nil {
		t.Fatalf("scan injected source: %v", err)
	}
	if len(registrations) != 1 {
		t.Fatalf("registrations = %v, want one", registrations)
	}
	err = ValidateCoverage(registrations, map[string]Declaration{})
	if err == nil {
		t.Fatal("unclassified injected GET unexpectedly passed")
	}
	if !strings.Contains(err.Error(), "unclassified runtime GET registration") {
		t.Fatalf("coverage error = %v", err)
	}

	registration := registrations[0]
	err = ValidateCoverage(registrations, map[string]Declaration{
		registration.Fingerprint: {
			Classification: ClassificationPage,
			OpenAPIPath:    "/new-directory",
			OperationID:    "listNewDirectory",
		},
	})
	if err != nil {
		t.Fatalf("classified injected GET failed: %v", err)
	}
}

func TestRegisterRoutesFilterIgnoresOrdinaryHandlerGETCalls(t *testing.T) {
	source := []byte(`package handlers

func (handler *Handler) Serve() {
	handler.client.GET("/not-a-route")
}

func (handler *Handler) RegisterRoutes(router *Router) {
	router.GET("/directory", handler.List)
}
`)
	registrations, err := scanSource(
		"internal/handlers/example.go",
		source,
		false,
	)
	if err != nil {
		t.Fatalf("scan handler source: %v", err)
	}
	if len(registrations) != 1 ||
		registrations[0].PathExpression != `"/directory"` {
		t.Fatalf("registrations = %#v", registrations)
	}
}

func TestCoverageRejectsStaleDeclaration(t *testing.T) {
	err := ValidateCoverage(nil, map[string]Declaration{
		"internal/app/app.go|Run|router.GET|\"/removed\"": {
			Classification: ClassificationNonList,
			OpenAPIPath:    "/removed",
			OperationID:    "getRemoved",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "stale runtime GET declaration") {
		t.Fatalf("coverage error = %v", err)
	}
}

func TestRouteFingerprintNormalizesStringLiteralSyntax(t *testing.T) {
	quoted, err := scanSource(
		"internal/app/example.go",
		[]byte(`package app
func registerRoutes(router *Router) { router.GET("/directory", handler) }
`),
		true,
	)
	if err != nil {
		t.Fatalf("scan quoted path: %v", err)
	}
	raw, err := scanSource(
		"internal/app/example.go",
		[]byte("package app\nfunc registerRoutes(router *Router) { router.GET(`/directory`, handler) }\n"),
		true,
	)
	if err != nil {
		t.Fatalf("scan raw path: %v", err)
	}
	if len(quoted) != 1 || len(raw) != 1 ||
		quoted[0].Fingerprint != raw[0].Fingerprint {
		t.Fatalf("quoted=%v raw=%v", quoted, raw)
	}
}

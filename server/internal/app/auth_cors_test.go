package app

import (
	"reflect"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/config"
)

func TestBrowserCredentialCORSNeverFallsBackToAllOrigins(t *testing.T) {
	for _, environment := range []string{"development", "production"} {
		t.Run(environment, func(t *testing.T) {
			cors := buildCORSConfig(&config.Config{
				Server: config.ServerConfig{
					Environment: environment,
				},
				CORS: config.CORSConfig{
					AllowedOrigins: []string{
						"*",
						"https://web.example.test/",
						"https://web.example.test",
					},
					AllowedMethods: []string{"GET", "POST"},
					AllowedHeaders: []string{"Content-Type", "Origin"},
				},
			})
			if cors.AllowAllOrigins {
				t.Fatal("credentialed CORS enabled all origins")
			}
			if !cors.AllowCredentials {
				t.Fatal("browser credential CORS disabled credentials")
			}
			if want := []string{"https://web.example.test"}; !reflect.DeepEqual(
				cors.AllowOrigins,
				want,
			) {
				t.Fatalf(
					"credentialed CORS origins = %v, want %v",
					cors.AllowOrigins,
					want,
				)
			}
		})
	}
}

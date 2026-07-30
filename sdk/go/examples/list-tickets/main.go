package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/seaworld008/chronodesk/sdk/go/chronodesk"
)

func main() {
	baseURL := requiredEnvironment("CHRONODESK_URL")
	projectKey := requiredEnvironment("CHRONODESK_PROJECT_KEY")
	clientID := requiredEnvironment("CHRONODESK_CLIENT_ID")
	clientSecret := requiredEnvironment("CHRONODESK_CLIENT_SECRET")

	anonymous, err := chronodesk.New(baseURL, projectKey)
	if err != nil {
		log.Fatal(err)
	}
	token, err := anonymous.ExchangeClientCredentials(
		context.Background(),
		chronodesk.ClientCredentials{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Audience:     chronodesk.AudienceAPI,
			Scopes:       []string{"tickets:read"},
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	client, err := anonymous.WithToken(token.AccessToken)
	if err != nil {
		log.Fatal(err)
	}
	result, err := client.ListTickets(
		context.Background(),
		chronodesk.TicketListOptions{Limit: 20},
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf(
		"project=%s tickets=%d request_id=%s\n",
		projectKey,
		len(result.Data),
		result.Meta.RequestID,
	)
}

func requiredEnvironment(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("required environment variable %s is not set", name)
	}
	return value
}

package main

import (
	"log"

	"github.com/seaworld008/chronodesk/server/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

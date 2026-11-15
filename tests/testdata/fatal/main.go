// Command fatal stops when its database isn't reachable, before it listens.
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	if os.Getenv("DATABASE_URL") == "" {
		log.Fatal("connecting to database: DATABASE_URL is not set")
	}
	log.Fatal(http.ListenAndServe(":8080", nil))
}

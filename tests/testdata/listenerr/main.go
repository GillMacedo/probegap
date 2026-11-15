// Command listenerr fails to listen on a bad address.
package main

import (
	"log"
	"net/http"
)

func main() {
	log.Fatal(http.ListenAndServe("8080", nil))
}

// Command panics crashes before it listens.
package main

import "net/http"

var routes map[string]http.Handler

func main() {
	routes["/healthz"] = http.NotFoundHandler()
	http.ListenAndServe(":8080", nil)
}

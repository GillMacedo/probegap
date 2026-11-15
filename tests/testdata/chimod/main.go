// Command chimod serves its health checks with go-chi/chi, mounted under
// a sub-router, the way many services do. It tests probegap with a real
// third-party router.
package main

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("chimod")) })
	r.Route("/internal", func(r chi.Router) {
		r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
		r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ready")) })
	})
	r.Group(func(r chi.Router) {
		r.Use(middleware.BasicAuth("admin", map[string]string{"admin": "secret"}))
		r.Get("/admin/status", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("up")) })
	})
	log.Fatal(http.ListenAndServe(":3000", r))
}

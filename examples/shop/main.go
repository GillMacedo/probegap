// Command shop is a made-up service for the probegap examples. It serves
// its API on -addr and metrics and a liveness check on -admin-addr, which
// defaults to localhost only.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	addr := flag.String("addr", ":8080", "API listen address")
	adminAddr := flag.String("admin-addr", "127.0.0.1:9090", "admin listen address")
	flag.Parse()
	token := os.Getenv("API_TOKEN")

	api := http.NewServeMux()
	api.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	api.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ready\n"))
	})
	api.Handle("/api/", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{}\n"))
	})))

	admin := http.NewServeMux()
	admin.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	admin.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# no metrics yet\n"))
	})

	go func() {
		log.Fatal(http.ListenAndServe(*adminAddr, admin))
	}()
	srv := &http.Server{Addr: *addr, Handler: api, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("shop listening on %s, admin on %s", *addr, *adminAddr)
	log.Fatal(srv.ListenAndServe())
}

func requireToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

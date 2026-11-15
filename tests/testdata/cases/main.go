// Command cases serves one route for each kind of probe result that
// probegap reports. The tests in tests/ point probes at it.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type ctxKey struct{}

func main() {
	addr := flag.String("addr", ":8080", "main listen address")
	tlsAddr := flag.String("tls-addr", ":8443", "HTTPS listen address")
	rawAddr := flag.String("raw-addr", ":7000", "a plain TCP listener that isn't HTTP")
	hostAddr := flag.String("host-addr", "", "a listener on a host taken from POD_IP, if set")
	late := flag.Duration("late", 0, "start the main server only after this long")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ok", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/any", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("POST /post-only", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("GET /forbidden", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusForbidden) })
	mux.HandleFunc("GET /bad", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "bad", http.StatusBadRequest) })
	mux.HandleFunc("GET /unavailable", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "database not reachable", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("GET /panics", func(w http.ResponseWriter, r *http.Request) {
		var m map[string]int
		m["x"] = 1
	})
	mux.HandleFunc("GET /old", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusMovedPermanently) })
	mux.HandleFunc("GET /old-missing", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/gone", http.StatusFound) })
	mux.HandleFunc("GET /loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	mux.HandleFunc("GET /elsewhere", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://login.example.com/", http.StatusFound)
	})
	mux.HandleFunc("GET /needs-header", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Probe") != "yes" {
			http.Error(w, "missing header", http.StatusUnauthorized)
		}
	})
	mux.HandleFunc("GET /kubelet-only", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.UserAgent(), "kube-probe/") {
			http.Error(w, "not the kubelet", http.StatusForbidden)
		}
	})
	mux.HandleFunc("GET /base-context", func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(ctxKey{}) != "set" {
			http.Error(w, "no base context", http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("GET /query", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("full") != "1" {
			http.Error(w, "want full=1", http.StatusBadRequest)
		}
	})
	mux.HandleFunc("internal.example.com/vhost", func(w http.ResponseWriter, r *http.Request) {})

	local := http.NewServeMux()
	local.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {})

	go func() { log.Fatal(http.ListenAndServe("127.0.0.1:9090", local)) }()
	go func() { log.Fatal(http.ListenAndServe("[::1]:9091", local)) }()
	go func() {
		// The certificate files don't exist; probegap never loads them.
		srv := &http.Server{Addr: *tlsAddr, Handler: mux, TLSConfig: &tls.Config{}}
		log.Fatal(srv.ListenAndServeTLS("/etc/tls/tls.crt", "/etc/tls/tls.key"))
	}()
	go func() {
		ln, err := net.Listen("tcp", *rawAddr)
		if err != nil {
			log.Fatal(err)
		}
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if *hostAddr != "" {
		go func() {
			log.Fatal(http.ListenAndServe(os.Getenv("POD_IP")+*hostAddr, mux))
		}()
		go func() {
			log.Fatal(http.ListenAndServe("192.168.50.5"+*hostAddr+"1", mux))
		}()
	}
	time.Sleep(*late)
	srv := &http.Server{
		Addr:        *addr,
		Handler:     mux,
		BaseContext: func(net.Listener) context.Context { return context.WithValue(context.Background(), ctxKey{}, "set") },
	}
	log.Fatal(srv.ListenAndServe())
}

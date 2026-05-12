package main

import (
	_ "embed"
	"log"
	"net/http"
	"os"

	"github.com/techtimo/spotmap-proxy/internal/admin"
	"github.com/techtimo/spotmap-proxy/internal/ais"
	"github.com/techtimo/spotmap-proxy/internal/db"
	"github.com/techtimo/spotmap-proxy/internal/metrics"
	"github.com/techtimo/spotmap-proxy/internal/ogn"
	"github.com/techtimo/spotmap-proxy/internal/provision"
	"github.com/techtimo/spotmap-proxy/internal/zoleo"
)

//go:embed static/index.html
var indexHTML []byte

func main() {
	db.Init()
	metrics.Start()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleIndex)
	mux.HandleFunc("GET /health", handleHealth)
	mux.Handle("/zoleo", zoleo.Handler())
	mux.Handle("/provision/", provision.Handler(ogn.Default, ais.Default))
	mux.Handle("/admin/", admin.Handler(ogn.Default, ais.Default))

	go ogn.Default.Connect()
	go ais.Default.Connect()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("[http] listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

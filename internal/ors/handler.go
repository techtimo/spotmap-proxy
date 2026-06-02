package ors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

const orsBase = "https://api.openrouteservice.org/v2/isochrones"

func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /{profile}", handle)
	return http.StripPrefix("/isochrones", mux)
}

func handle(w http.ResponseWriter, r *http.Request) {
	apiKey := os.Getenv("ORS_API_KEY")
	if apiKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "isochrone service not configured"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	profile := r.PathValue("profile")
	upstream := fmt.Sprintf("%s/%s", orsBase, profile)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstream, bytes.NewReader(body))
	if err != nil {
		log.Printf("[ors] build request error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[ors] upstream error: %v", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[ors] read response error: %v", err)
		http.Error(w, "upstream read error", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

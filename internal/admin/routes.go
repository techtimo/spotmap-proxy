package admin

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/techtimo/spotmap-proxy/internal/db"
)

type Reconnector interface {
	Reconnect()
}

func Handler(ogn, ais Reconnector) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /routes", bearer(listRoutes))
	mux.HandleFunc("DELETE /routes/zoleo/{id}", bearer(deleteZoleoRoute))
	mux.HandleFunc("DELETE /routes/ogn/{address_type}/{device_id}", bearer(deleteOgnRoute(ogn)))
	mux.HandleFunc("DELETE /routes/ais/{mmsi}", bearer(deleteAisRoute(ais)))
	return http.StripPrefix("/admin", mux)
}

func bearer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := os.Getenv("ADMIN_KEY")
		if key == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			return
		}
		next(w, r)
	}
}

func listRoutes(w http.ResponseWriter, r *http.Request) {
	zoleo := db.ListZoleoRoutes()
	for i := range zoleo {
		zoleo[i].IngestKey = redact(zoleo[i].IngestKey)
	}
	ogn := db.ListOgnRoutes()
	for i := range ogn {
		ogn[i].IngestKey = redact(ogn[i].IngestKey)
	}
	ais := db.ListAisRoutes()
	for i := range ais {
		ais[i].IngestKey = redact(ais[i].IngestKey)
	}
	writeJSON(w, http.StatusOK, map[string]any{"zoleo": zoleo, "ogn": ogn, "ais": ais})
}

func deleteZoleoRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, err := db.DeleteZoleoRoute(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func deleteOgnRoute(ogn Reconnector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addressType := r.PathValue("address_type")
		deviceID := r.PathValue("device_id")
		n, err := db.DeleteOgnRoute(addressType, deviceID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if n == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
			return
		}
		ogn.Reconnect()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func redact(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return key[:4] + "****"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

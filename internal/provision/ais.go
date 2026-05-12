package provision

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/techtimo/spotmap-proxy/internal/db"
)

func createAisRoute(ais Reconnector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MMSI         string `json:"mmsi"`
			WordpressURL string `json:"wordpress_url"`
			IngestKey    string `json:"ingest_key"`
			Label        string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.MMSI == "" || body.WordpressURL == "" || body.IngestKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mmsi, wordpress_url, and ingest_key are required"})
			return
		}

		parsed, err := strconv.ParseInt(body.MMSI, 10, 64)
		if err != nil || parsed <= 0 || parsed > 999999999 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mmsi must be a valid 1-9 digit number"})
			return
		}
		mmsi := fmt.Sprintf("%d", parsed)

		if existing := db.GetAisRoute(mmsi); existing != nil {
			if existing.IngestKey != body.IngestKey {
				log.Printf("[provision] ais %s rejected: already registered to a different site", mmsi)
				writeJSON(w, http.StatusConflict, map[string]string{"error": "device already registered to a different site"})
				return
			}
		} else if db.CountAllRoutes() >= maxRoutes() {
			log.Printf("[provision] ais %s rejected: route limit reached (%d)", mmsi, maxRoutes())
			writeJSON(w, http.StatusConflict, map[string]string{"error": "route limit reached"})
			return
		}

		ip := clientIP(r)
		if !checkRateLimit(ip) {
			log.Printf("[provision] ais %s rejected: rate limit exceeded for %s", mmsi, ip)
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests"})
			return
		}

		if err := db.UpsertAisRoute(db.AisRoute{
			MMSI:         mmsi,
			WordpressURL: body.WordpressURL,
			IngestKey:    body.IngestKey,
			Label:        body.Label,
			CreatedAt:    time.Now().Unix(),
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		recordSuccess(ip)
		ais.Reconnect()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func deleteAisRoute(ais Reconnector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mmsi := r.PathValue("mmsi")
		var body struct {
			IngestKey string `json:"ingest_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IngestKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ingest_key is required"})
			return
		}
		n, err := db.DeleteAisRouteByKey(mmsi, body.IngestKey)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if n == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
			return
		}
		ais.Reconnect()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

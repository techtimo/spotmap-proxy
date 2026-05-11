package provision

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/techtimo/spotmap-proxy/internal/db"
)

type Reconnector interface {
	Reconnect()
}

var (
	rateMu  sync.Mutex
	rateMap = map[string][]time.Time{}
)

func maxRoutes() int {
	n, err := strconv.Atoi(os.Getenv("MAX_ROUTES"))
	if err != nil || n <= 0 {
		return 100
	}
	return n
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func checkRateLimit(ip string) bool {
	rateMu.Lock()
	defer rateMu.Unlock()
	cutoff := time.Now().Add(-time.Minute)
	var recent []time.Time
	for _, t := range rateMap[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(rateMap, ip)
	} else {
		rateMap[ip] = recent
	}
	return len(recent) < 2
}

func recordSuccess(ip string) {
	rateMu.Lock()
	rateMap[ip] = append(rateMap[ip], time.Now())
	rateMu.Unlock()
}

func Handler(ogn Reconnector) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /routes/zoleo", bearer(createZoleoRoute))
	mux.HandleFunc("DELETE /routes/zoleo/{id}", bearer(deleteZoleoRoute))
	mux.HandleFunc("POST /routes/ogn", bearer(createOgnRoute(ogn)))
	mux.HandleFunc("DELETE /routes/ogn/{device_id}", bearer(deleteOgnRouteByDevice(ogn)))
	mux.HandleFunc("DELETE /routes/ogn/{device_id}/{address_type}", bearer(deleteOgnRoute(ogn)))
	return http.StripPrefix("/provision", mux)
}

func bearer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := os.Getenv("PROVISION_KEY")
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

func createZoleoRoute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ZoleoAccountID string `json:"zoleo_account_id"`
		WordpressURL   string `json:"wordpress_url"`
		IngestKey      string `json:"ingest_key"`
		Label          string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ZoleoAccountID == "" || body.WordpressURL == "" || body.IngestKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "zoleo_account_id, wordpress_url, and ingest_key are required"})
		return
	}

	if existing := db.GetZoleoRoute(body.ZoleoAccountID); existing != nil {
		if existing.IngestKey != body.IngestKey {
			log.Printf("[provision] zoleo %s rejected: already registered to a different site", body.ZoleoAccountID)
			writeJSON(w, http.StatusConflict, map[string]string{"error": "device already registered to a different site"})
			return
		}
	} else if db.CountZoleoRoutes()+db.CountOgnRoutes() >= maxRoutes() {
		log.Printf("[provision] zoleo %s rejected: route limit reached (%d)", body.ZoleoAccountID, maxRoutes())
		writeJSON(w, http.StatusConflict, map[string]string{"error": "device already registered to a different site"})
		return
	}

	ip := clientIP(r)
	if !checkRateLimit(ip) {
		log.Printf("[provision] zoleo %s rejected: rate limit exceeded for %s", body.ZoleoAccountID, ip)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests"})
		return
	}

	if err := db.UpsertZoleoRoute(db.ZoleoRoute{
		ZoleoAccountID: body.ZoleoAccountID,
		WordpressURL:   body.WordpressURL,
		IngestKey:      body.IngestKey,
		Label:          body.Label,
		CreatedAt:      time.Now().Unix(),
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	recordSuccess(ip)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func deleteZoleoRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		IngestKey string `json:"ingest_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IngestKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ingest_key is required"})
		return
	}
	n, err := db.DeleteZoleoRouteByKey(id, body.IngestKey)
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

func createOgnRoute(ogn Reconnector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AddressType  string `json:"address_type"`
			DeviceID     string `json:"device_id"`
			WordpressURL string `json:"wordpress_url"`
			IngestKey    string `json:"ingest_key"`
			Label        string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DeviceID == "" || body.WordpressURL == "" || body.IngestKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device_id, wordpress_url, and ingest_key are required"})
			return
		}

		upperDevID := strings.ToUpper(body.DeviceID)

		if body.AddressType != "" {
			// Single-prefix registration (existing behaviour).
			upper := strings.ToUpper(body.AddressType)
			if !slices.Contains(db.OgnPrefixes, upper) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "address_type must be one of: " + strings.Join(db.OgnPrefixes, ", ")})
				return
			}
			if existing := db.GetOgnRoute(upper, upperDevID); existing != nil {
				if existing.IngestKey != body.IngestKey {
					log.Printf("[provision] ogn %s/%s rejected: already registered to a different site", upper, upperDevID)
					writeJSON(w, http.StatusConflict, map[string]string{"error": "device already registered to a different site"})
					return
				}
			} else if db.CountZoleoRoutes()+db.CountOgnRoutes() >= maxRoutes() {
				log.Printf("[provision] ogn %s/%s rejected: route limit reached (%d)", upper, upperDevID, maxRoutes())
				writeJSON(w, http.StatusConflict, map[string]string{"error": "route limit reached"})
				return
			}
			ip := clientIP(r)
			if !checkRateLimit(ip) {
				log.Printf("[provision] ogn %s/%s rejected: rate limit exceeded for %s", upper, upperDevID, ip)
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests"})
				return
			}
			if err := db.UpsertOgnRoute(db.OgnRoute{
				AddressType:  upper,
				DeviceID:     upperDevID,
				WordpressURL: body.WordpressURL,
				IngestKey:    body.IngestKey,
				Label:        body.Label,
				CreatedAt:    time.Now().Unix(),
			}); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			recordSuccess(ip)
			ogn.Reconnect()
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}

		// Dual-prefix registration: FLR + ICA.
		dualPrefixes := []string{"FLR", "ICA"}
		for _, prefix := range dualPrefixes {
			if existing := db.GetOgnRoute(prefix, upperDevID); existing != nil && existing.IngestKey != body.IngestKey {
				log.Printf("[provision] ogn %s/%s rejected: already registered to a different site", prefix, upperDevID)
				writeJSON(w, http.StatusConflict, map[string]string{"error": "device already registered to a different site"})
				return
			}
		}
		newCount := 0
		for _, prefix := range dualPrefixes {
			if db.GetOgnRoute(prefix, upperDevID) == nil {
				newCount++
			}
		}
		if db.CountZoleoRoutes()+db.CountOgnRoutes()+newCount > maxRoutes() {
			log.Printf("[provision] ogn %s rejected: route limit reached (%d)", upperDevID, maxRoutes())
			writeJSON(w, http.StatusConflict, map[string]string{"error": "route limit reached"})
			return
		}
		ip := clientIP(r)
		if !checkRateLimit(ip) {
			log.Printf("[provision] ogn %s rejected: rate limit exceeded for %s", upperDevID, ip)
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests"})
			return
		}
		for _, prefix := range dualPrefixes {
			if err := db.UpsertOgnRoute(db.OgnRoute{
				AddressType:  prefix,
				DeviceID:     upperDevID,
				WordpressURL: body.WordpressURL,
				IngestKey:    body.IngestKey,
				Label:        body.Label,
				CreatedAt:    time.Now().Unix(),
			}); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		recordSuccess(ip)
		ogn.Reconnect()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func deleteOgnRoute(ogn Reconnector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		addressType := r.PathValue("address_type")
		var body struct {
			IngestKey string `json:"ingest_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IngestKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ingest_key is required"})
			return
		}
		n, err := db.DeleteOgnRouteByKey(addressType, deviceID, body.IngestKey)
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

func deleteOgnRouteByDevice(ogn Reconnector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		var body struct {
			IngestKey string `json:"ingest_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IngestKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ingest_key is required"})
			return
		}
		n, err := db.DeleteOgnRoutesByDeviceID(deviceID, body.IngestKey)
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

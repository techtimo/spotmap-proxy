package zoleo

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/techtimo/spotmap-proxy/internal/db"
	"github.com/techtimo/spotmap-proxy/internal/forwarder"
)

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if !checkBasicAuth(w, r) {
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		ts := time.Now().UTC().Format(time.RFC3339Nano)
		log.Printf("[zoleo] received: %s", body)
		appendLog(ts, body)

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			jsonOK(w)
			return
		}

		accountID := firstString(payload, "accountId", "account_id", "zoleo_account_id")
		if accountID == "" {
			log.Println("[zoleo] could not determine zoleo_account_id from payload — tried accountId, account_id, zoleo_account_id")
			jsonOK(w)
			return
		}

		route := db.GetZoleoRoute(accountID)
		if route == nil {
			log.Printf("[zoleo] no route for account ID: %s", accountID)
			jsonOK(w)
			return
		}

		go forwarder.Post(route.WordpressURL, route.IngestKey, body)
		jsonOK(w)
	})
}

func checkBasicAuth(w http.ResponseWriter, r *http.Request) bool {
	user := os.Getenv("ZOLEO_BASIC_AUTH_USER")
	if user == "" {
		return true
	}
	pass := os.Getenv("ZOLEO_BASIC_AUTH_PASS")

	header := r.Header.Get("Authorization")
	scheme, encoded, _ := strings.Cut(header, " ")
	if !strings.EqualFold(scheme, "Basic") || encoded == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="zoleo"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="zoleo"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return false
	}
	u, p, _ := strings.Cut(string(decoded), ":")
	if u != user || p != pass {
		w.Header().Set("WWW-Authenticate", `Basic realm="zoleo"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return false
	}
	return true
}

func appendLog(ts string, body []byte) {
	path := os.Getenv("ZOLEO_LOG_PATH")
	if path == "" {
		path = "/data/zoleo-payloads.log"
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[zoleo] failed to open payload log: %v", err)
		return
	}
	defer f.Close()
	f.WriteString(ts + " " + string(body) + "\n")
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func jsonOK(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

package admin

import (
	"net/http"

	"github.com/techtimo/spotmap-proxy/internal/db"
)

func deleteAisRoute(ais Reconnector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mmsi := r.PathValue("mmsi")
		n, err := db.DeleteAisRoute(mmsi)
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

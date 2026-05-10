package metrics

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/techtimo/spotmap-proxy/internal/db"
)

var (
	registry = prometheus.NewRegistry()

	latGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ogn_aircraft_lat",
		Help: "Latest latitude of OGN aircraft",
	}, []string{"identifier"})

	lonGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ogn_aircraft_lon",
		Help: "Latest longitude of OGN aircraft",
	}, []string{"identifier"})

	altGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ogn_aircraft_alt_meters",
		Help: "Latest altitude in meters of OGN aircraft",
	}, []string{"identifier"})

	lastSeenGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ogn_aircraft_last_seen_timestamp",
		Help: "Unix timestamp of last OGN position update",
	}, []string{"identifier"})

	mu       sync.Mutex
	lastSeen = map[string]int64{}
)

func init() {
	registry.MustRegister(
		latGauge, lonGauge, altGauge, lastSeenGauge,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "spotmap_zoleo_routes_total",
			Help: "Number of Zoleo routes currently registered",
		}, func() float64 { return float64(db.CountZoleoRoutes()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "spotmap_ogn_routes_total",
			Help: "Number of OGN routes currently registered",
		}, func() float64 { return float64(db.CountOgnRoutes()) }),
	)
}

func Update(identifier string, lat, lon float64, altM *float64) {
	now := time.Now().Unix()
	labels := prometheus.Labels{"identifier": identifier}

	latGauge.With(labels).Set(lat)
	lonGauge.With(labels).Set(lon)
	if altM != nil {
		altGauge.With(labels).Set(*altM)
	}
	lastSeenGauge.With(labels).Set(float64(now))

	mu.Lock()
	lastSeen[identifier] = now
	mu.Unlock()
}

func staleSeconds() int64 {
	s, err := strconv.ParseInt(os.Getenv("OGN_STALE_SECONDS"), 10, 64)
	if err != nil || s <= 0 {
		return 300
	}
	return s
}

func cleanupLoop() {
	for range time.Tick(30 * time.Second) {
		cutoff := time.Now().Unix() - staleSeconds()
		mu.Lock()
		for id, ts := range lastSeen {
			if ts < cutoff {
				l := prometheus.Labels{"identifier": id}
				latGauge.Delete(l)
				lonGauge.Delete(l)
				altGauge.Delete(l)
				lastSeenGauge.Delete(l)
				delete(lastSeen, id)
			}
		}
		mu.Unlock()
	}
}

func Start() {
	port := os.Getenv("METRICS_PORT")
	if port == "" {
		port = "9877"
	}

	go cleanupLoop()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	go func() {
		log.Printf("[metrics] listening on :%s", port)
		if err := http.ListenAndServe(":"+port, mux); err != nil {
			log.Fatalf("[metrics] %v", err)
		}
	}()
}

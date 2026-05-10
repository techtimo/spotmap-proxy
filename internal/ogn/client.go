package ogn

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/techtimo/spotmap-proxy/internal/db"
	"github.com/techtimo/spotmap-proxy/internal/forwarder"
	"github.com/techtimo/spotmap-proxy/internal/metrics"
)

const (
	ognHost = "aprs.glidernet.org"
	ognPort = 14580
)

// APRS position body regex — matches the same pattern as the Node.js original.
var aprsRe = regexp.MustCompile(
	`^[@/](\d{2})(\d{2})(\d{2})[hz](\d{2})(\d{2}\.\d{2})([NS])[/\\](\d{3})(\d{2}\.\d{2})([EW]).(\d{3})/(\d{3})(?:/A=(\d+))?`,
)

type point struct {
	callsign  string
	lat       float64
	lon       float64
	timestamp int64
	course    int
	speedKmh  int
	altM      *float64
}

func parseAprs(line string) *point {
	colonIdx := strings.IndexByte(line, ':')
	if colonIdx == -1 {
		return nil
	}
	gtIdx := strings.IndexByte(line, '>')
	if gtIdx == -1 || gtIdx > colonIdx {
		return nil
	}
	callsign := line[:gtIdx]
	body := line[colonIdx+1:]

	m := aprsRe.FindStringSubmatch(body)
	if m == nil {
		return nil
	}

	hh, _ := strconv.Atoi(m[1])
	mm, _ := strconv.Atoi(m[2])
	ss, _ := strconv.Atoi(m[3])

	latDeg, _ := strconv.ParseFloat(m[4], 64)
	latMin, _ := strconv.ParseFloat(m[5], 64)
	lat := latDeg + latMin/60
	if m[6] == "S" {
		lat = -lat
	}

	lonDeg, _ := strconv.ParseFloat(m[7], 64)
	lonMin, _ := strconv.ParseFloat(m[8], 64)
	lon := lonDeg + lonMin/60
	if m[9] == "W" {
		lon = -lon
	}

	now := time.Now().UTC()
	ts := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, ss, 0, time.UTC)
	if ts.After(now) {
		ts = ts.AddDate(0, 0, -1)
	}

	course, _ := strconv.Atoi(m[10])
	speed, _ := strconv.Atoi(m[11])
	speedKmh := int(math.Round(float64(speed) * 1.852))

	var altPtr *float64
	if m[12] != "" {
		altFt, _ := strconv.ParseFloat(m[12], 64)
		altM := math.Round(altFt * 0.3048)
		altPtr = &altM
	}

	return &point{
		callsign:  callsign,
		lat:       math.Round(lat*1e6) / 1e6,
		lon:       math.Round(lon*1e6) / 1e6,
		timestamp: ts.Unix(),
		course:    course,
		speedKmh:  speedKmh,
		altM:      altPtr,
	}
}

func buildFilter(ids []string) string {
	if len(ids) == 0 {
		return "#filter -1"
	}
	return "#filter p/" + strings.Join(ids, "/")
}

type Client struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

var Default = &Client{}

func (c *Client) Connect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.start()
}

func (c *Client) Reconnect() {
	log.Println("[ogn] route change detected, reconnecting")
	c.mu.Lock()
	defer c.mu.Unlock()
	c.start()
}

func (c *Client) start() {
	if c.cancel != nil {
		c.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go c.run(ctx)
}

func (c *Client) run(ctx context.Context) {
	for {
		c.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}

func (c *Client) runOnce(ctx context.Context) {
	flarmIDs := db.GetAllOgnFlarmIDs()
	filter := buildFilter(flarmIDs)
	addr := net.JoinHostPort(ognHost, strconv.Itoa(ognPort))

	log.Printf("[ogn] connecting to %s (%d routes)", addr, len(flarmIDs))

	conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		log.Printf("[ogn] connect: %v", err)
		return
	}
	defer conn.Close()

	// Close conn when context is cancelled (Reconnect call).
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	log.Println("[ogn] connected, logging in")
	fmt.Fprintf(conn, "user NOCALL pass -1 vers SpotmapProxy 1.0\r\n")
	conn.SetDeadline(time.Now().Add(90 * time.Second))

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		conn.SetDeadline(time.Now().Add(90 * time.Second))
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#") {
			log.Printf("[ogn] %s", line)
			if strings.Contains(line, "logresp") {
				log.Printf("[ogn] sending filter: %s", filter)
				fmt.Fprintf(conn, "%s\r\n", filter)
			}
			continue
		}

		hasPrefix := false
		for _, p := range db.OgnPrefixes {
			if strings.HasPrefix(line, p) {
				hasPrefix = true
				break
			}
		}
		if !hasPrefix {
			continue
		}

		pt := parseAprs(line)
		if pt == nil {
			log.Printf("[ogn] could not parse: %s", line)
			continue
		}

		metrics.Update(pt.callsign, pt.lat, pt.lon, pt.altM)

		route := db.GetOgnRoute(pt.callsign[:3], pt.callsign[3:])
		if route == nil {
			continue
		}

		var altVal any
		if pt.altM != nil {
			altVal = *pt.altM
		}
		log.Printf("[ogn] %s lat=%g lon=%g alt=%vm", pt.callsign, pt.lat, pt.lon, altVal)

		payload, _ := json.Marshal(map[string]any{
			"lat":          pt.lat,
			"lon":          pt.lon,
			"timestamp":    pt.timestamp,
			"altitude":     altVal,
			"speed":        pt.speedKmh,
			"course":       pt.course,
			"address_type": route.AddressType,
			"device_id":    route.DeviceID,
		})
		go forwarder.Post(route.WordpressURL, route.IngestKey, payload)
	}

	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		log.Printf("[ogn] read error: %v", err)
	}
}

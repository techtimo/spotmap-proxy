package ais

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/techtimo/spotmap-proxy/internal/db"
	"github.com/techtimo/spotmap-proxy/internal/forwarder"
	"github.com/techtimo/spotmap-proxy/internal/metrics"
)

const aisStreamURL = "wss://stream.aisstream.io/v0/stream"

type aisMsg struct {
	MessageType string `json:"MessageType"`
	MetaData    struct {
		MMSI         int    `json:"MMSI"`
		ShipName     string `json:"ShipName"`
		TimeReceived string `json:"TimeReceived"`
	} `json:"MetaData"`
	Message struct {
		PositionReport struct {
			Latitude    float64 `json:"Latitude"`
			Longitude   float64 `json:"Longitude"`
			Sog         float64 `json:"Sog"`
			Cog         float64 `json:"Cog"`
			TrueHeading int     `json:"TrueHeading"`
		} `json:"PositionReport"`
	} `json:"Message"`
}

type subscribeMsg struct {
	APIKey             string         `json:"APIKey"`
	BoundingBoxes      [][][2]float64 `json:"BoundingBoxes"`
	FilterMessageTypes []string       `json:"FilterMessageTypes"`
	MMSI               []string       `json:"MMSI"`
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
	log.Println("[ais] route change detected, reconnecting")
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
	apiKey := os.Getenv("AIS_API_KEY")
	if apiKey == "" {
		log.Println("[ais] AIS_API_KEY not set, AIS disabled")
		return
	}
	for {
		c.runOnce(ctx, apiKey)
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}

func (c *Client) runOnce(ctx context.Context, apiKey string) {
	mmsis := db.GetAllAisMmsis()
	log.Printf("[ais] connecting to %s (%d routes)", aisStreamURL, len(mmsis))

	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := dialer.DialContext(ctx, aisStreamURL, nil)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("[ais] connect: %v", err)
		}
		return
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	sub := subscribeMsg{
		APIKey:             apiKey,
		BoundingBoxes:      [][][2]float64{},
		FilterMessageTypes: []string{"PositionReport"},
		MMSI:               mmsis,
	}
	if err := conn.WriteJSON(sub); err != nil {
		log.Printf("[ais] subscribe: %v", err)
		return
	}
	log.Printf("[ais] subscribed to %d MMSIs", len(mmsis))

	// AISstream drops the connection immediately if the API key is invalid.
	_, firstMsg, err := conn.ReadMessage()
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("[ais] auth failed or connection closed immediately: %v — check AIS_API_KEY", err)
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Minute):
			}
		}
		return
	}
	var errCheck struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(firstMsg, &errCheck) == nil && errCheck.Error != "" {
		log.Printf("[ais] server error: %s — check AIS_API_KEY", errCheck.Error)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Minute):
		}
		return
	}
	c.handle(firstMsg)

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[ais] read: %v", err)
			}
			return
		}
		c.handle(msg)
	}
}

func (c *Client) handle(raw []byte) {
	var m aisMsg
	if err := json.Unmarshal(raw, &m); err != nil {
		log.Printf("[ais] unmarshal: %v", err)
		return
	}
	if m.MessageType != "PositionReport" {
		return
	}

	mmsi := strconv.Itoa(m.MetaData.MMSI)
	pr := m.Message.PositionReport

	route := db.GetAisRoute(mmsi)
	if route == nil {
		return
	}

	metrics.UpdateAis(mmsi)

	ts := time.Now().Unix()
	if t, err := time.Parse(time.RFC3339Nano, m.MetaData.TimeReceived); err == nil {
		ts = t.Unix()
	}

	speedKmh := math.Round(pr.Sog*1.852*10) / 10
	log.Printf("[ais] %s (%s) lat=%g lon=%g sog=%.1fkm/h cog=%d",
		mmsi, strings.TrimSpace(m.MetaData.ShipName), pr.Latitude, pr.Longitude, speedKmh, int(pr.Cog))

	payload, _ := json.Marshal(map[string]any{
		"lat":       math.Round(pr.Latitude*1e6) / 1e6,
		"lon":       math.Round(pr.Longitude*1e6) / 1e6,
		"timestamp": ts,
		"speed":     speedKmh,
		"course":    int(math.Round(pr.Cog)),
		"heading":   pr.TrueHeading,
		"mmsi":      mmsi,
		"ship_name": strings.TrimSpace(m.MetaData.ShipName),
	})
	go forwarder.Post(route.WordpressURL, route.IngestKey, payload)
}

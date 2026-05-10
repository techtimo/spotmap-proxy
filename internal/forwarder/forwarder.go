package forwarder

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 10 * time.Second}

func Post(wordpressURL, ingestKey string, body []byte) {
	sep := "?"
	if strings.Contains(wordpressURL, "?") {
		sep = "&"
	}
	url := wordpressURL + sep + "key=" + ingestKey

	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[forwarder] error posting to %s: %v", wordpressURL, err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		log.Printf("[forwarder] HTTP %d from %s", resp.StatusCode, wordpressURL)
	}
}

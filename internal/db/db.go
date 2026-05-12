package db

import (
	"database/sql"
	"log"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

var OgnPrefixes = []string{"FLR", "ICA", "OGN", "FNT", "PAW"}

var conn *sql.DB

func Init() {
	path := os.Getenv("DATABASE_PATH")
	if path == "" {
		path = "/data/spotmap-proxy.sqlite"
	}

	var err error
	conn, err = sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("[db] open: %v", err)
	}
	conn.SetMaxOpenConns(1)

	_, err = conn.Exec(`
		CREATE TABLE IF NOT EXISTS zoleo_routes (
			zoleo_account_id TEXT PRIMARY KEY,
			wordpress_url    TEXT NOT NULL,
			ingest_key       TEXT NOT NULL,
			label            TEXT,
			created_at       INTEGER DEFAULT (unixepoch())
		);
		CREATE TABLE IF NOT EXISTS ogn_routes (
			address_type     TEXT NOT NULL,
			device_id        TEXT NOT NULL,
			wordpress_url    TEXT NOT NULL,
			ingest_key       TEXT NOT NULL,
			label            TEXT,
			created_at       INTEGER DEFAULT (unixepoch()),
			PRIMARY KEY (address_type, device_id)
		);
		CREATE TABLE IF NOT EXISTS ais_routes (
			mmsi          TEXT PRIMARY KEY,
			wordpress_url TEXT NOT NULL,
			ingest_key    TEXT NOT NULL,
			label         TEXT,
			created_at    INTEGER DEFAULT (unixepoch())
		);
	`)
	if err != nil {
		log.Fatalf("[db] schema: %v", err)
	}
	log.Printf("[db] opened %s", path)
}

type ZoleoRoute struct {
	ZoleoAccountID string `json:"zoleo_account_id"`
	WordpressURL   string `json:"wordpress_url"`
	IngestKey      string `json:"ingest_key"`
	Label          string `json:"label"`
	CreatedAt      int64  `json:"created_at"`
}

type OgnRoute struct {
	AddressType  string `json:"address_type"`
	DeviceID     string `json:"device_id"`
	WordpressURL string `json:"wordpress_url"`
	IngestKey    string `json:"ingest_key"`
	Label        string `json:"label"`
	CreatedAt    int64  `json:"created_at"`
}

func GetZoleoRoute(accountID string) *ZoleoRoute {
	var r ZoleoRoute
	err := conn.QueryRow(
		`SELECT zoleo_account_id, wordpress_url, ingest_key, COALESCE(label,''), created_at FROM zoleo_routes WHERE zoleo_account_id = ?`,
		accountID,
	).Scan(&r.ZoleoAccountID, &r.WordpressURL, &r.IngestKey, &r.Label, &r.CreatedAt)
	if err != nil {
		return nil
	}
	return &r
}

func ListZoleoRoutes() []ZoleoRoute {
	rows, err := conn.Query(`SELECT zoleo_account_id, wordpress_url, ingest_key, COALESCE(label,''), created_at FROM zoleo_routes ORDER BY created_at`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ZoleoRoute
	for rows.Next() {
		var r ZoleoRoute
		rows.Scan(&r.ZoleoAccountID, &r.WordpressURL, &r.IngestKey, &r.Label, &r.CreatedAt)
		out = append(out, r)
	}
	return out
}

func UpsertZoleoRoute(r ZoleoRoute) error {
	_, err := conn.Exec(`
		INSERT INTO zoleo_routes (zoleo_account_id, wordpress_url, ingest_key, label)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(zoleo_account_id) DO UPDATE SET
			wordpress_url = excluded.wordpress_url,
			ingest_key    = excluded.ingest_key,
			label         = excluded.label
	`, r.ZoleoAccountID, r.WordpressURL, r.IngestKey, nullStr(r.Label))
	return err
}

func DeleteZoleoRoute(accountID string) (int64, error) {
	res, err := conn.Exec(`DELETE FROM zoleo_routes WHERE zoleo_account_id = ?`, accountID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func DeleteZoleoRouteByKey(accountID, ingestKey string) (int64, error) {
	res, err := conn.Exec(`DELETE FROM zoleo_routes WHERE zoleo_account_id = ? AND ingest_key = ?`, accountID, ingestKey)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func GetOgnRoute(addressType, deviceID string) *OgnRoute {
	var r OgnRoute
	err := conn.QueryRow(
		`SELECT address_type, device_id, wordpress_url, ingest_key, COALESCE(label,''), created_at FROM ogn_routes WHERE address_type = ? AND device_id = ?`,
		strings.ToUpper(addressType), strings.ToUpper(deviceID),
	).Scan(&r.AddressType, &r.DeviceID, &r.WordpressURL, &r.IngestKey, &r.Label, &r.CreatedAt)
	if err != nil {
		return nil
	}
	return &r
}

func ListOgnRoutes() []OgnRoute {
	rows, err := conn.Query(`SELECT address_type, device_id, wordpress_url, ingest_key, COALESCE(label,''), created_at FROM ogn_routes ORDER BY created_at`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []OgnRoute
	for rows.Next() {
		var r OgnRoute
		rows.Scan(&r.AddressType, &r.DeviceID, &r.WordpressURL, &r.IngestKey, &r.Label, &r.CreatedAt)
		out = append(out, r)
	}
	return out
}

func UpsertOgnRoute(r OgnRoute) error {
	_, err := conn.Exec(`
		INSERT INTO ogn_routes (address_type, device_id, wordpress_url, ingest_key, label)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(address_type, device_id) DO UPDATE SET
			wordpress_url = excluded.wordpress_url,
			ingest_key    = excluded.ingest_key,
			label         = excluded.label
	`, strings.ToUpper(r.AddressType), strings.ToUpper(r.DeviceID), r.WordpressURL, r.IngestKey, nullStr(r.Label))
	return err
}

func DeleteOgnRoute(addressType, deviceID string) (int64, error) {
	res, err := conn.Exec(`DELETE FROM ogn_routes WHERE address_type = ? AND device_id = ?`, strings.ToUpper(addressType), strings.ToUpper(deviceID))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func DeleteOgnRouteByKey(addressType, deviceID, ingestKey string) (int64, error) {
	res, err := conn.Exec(`DELETE FROM ogn_routes WHERE address_type = ? AND device_id = ? AND ingest_key = ?`, strings.ToUpper(addressType), strings.ToUpper(deviceID), ingestKey)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func DeleteOgnRoutesByDeviceID(deviceID, ingestKey string) (int64, error) {
	res, err := conn.Exec(`DELETE FROM ogn_routes WHERE device_id = ? AND ingest_key = ?`, strings.ToUpper(deviceID), ingestKey)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func GetAllOgnFlarmIDs() []string {
	rows, err := conn.Query(`SELECT address_type || device_id FROM ogn_routes`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids
}

func CountZoleoRoutes() int { return countTable("zoleo_routes") }
func CountOgnRoutes() int   { return countTable("ogn_routes") }
func CountAisRoutes() int   { return countTable("ais_routes") }
func CountAllRoutes() int   { return CountZoleoRoutes() + CountOgnRoutes() + CountAisRoutes() }

type AisRoute struct {
	MMSI         string `json:"mmsi"`
	WordpressURL string `json:"wordpress_url"`
	IngestKey    string `json:"ingest_key"`
	Label        string `json:"label"`
	CreatedAt    int64  `json:"created_at"`
}

func GetAisRoute(mmsi string) *AisRoute {
	var r AisRoute
	err := conn.QueryRow(
		`SELECT mmsi, wordpress_url, ingest_key, COALESCE(label,''), created_at FROM ais_routes WHERE mmsi = ?`,
		mmsi,
	).Scan(&r.MMSI, &r.WordpressURL, &r.IngestKey, &r.Label, &r.CreatedAt)
	if err != nil {
		return nil
	}
	return &r
}

func ListAisRoutes() []AisRoute {
	rows, err := conn.Query(`SELECT mmsi, wordpress_url, ingest_key, COALESCE(label,''), created_at FROM ais_routes ORDER BY created_at`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []AisRoute
	for rows.Next() {
		var r AisRoute
		rows.Scan(&r.MMSI, &r.WordpressURL, &r.IngestKey, &r.Label, &r.CreatedAt)
		out = append(out, r)
	}
	return out
}

func UpsertAisRoute(r AisRoute) error {
	_, err := conn.Exec(`
		INSERT INTO ais_routes (mmsi, wordpress_url, ingest_key, label)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(mmsi) DO UPDATE SET
			wordpress_url = excluded.wordpress_url,
			ingest_key    = excluded.ingest_key,
			label         = excluded.label
	`, r.MMSI, r.WordpressURL, r.IngestKey, nullStr(r.Label))
	return err
}

func DeleteAisRoute(mmsi string) (int64, error) {
	res, err := conn.Exec(`DELETE FROM ais_routes WHERE mmsi = ?`, mmsi)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func DeleteAisRouteByKey(mmsi, ingestKey string) (int64, error) {
	res, err := conn.Exec(`DELETE FROM ais_routes WHERE mmsi = ? AND ingest_key = ?`, mmsi, ingestKey)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func GetAllAisMmsis() []string {
	rows, err := conn.Query(`SELECT mmsi FROM ais_routes`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids
}

func countTable(table string) int {
	if conn == nil {
		return 0
	}
	var n int
	conn.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
	return n
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

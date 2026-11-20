package clickhouse

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dudo/log-ingester/internal/logs"
)

//go:embed schema.sql
var schema string

const timestampLayout = "2006-01-02 15:04:05.000000"

type Store struct {
	endpoint             *url.URL
	database, user, pass string
	client               *http.Client
}

func envOr(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}

func OpenFromEnv() (*Store, error) {
	endpoint, err := url.Parse(envOr("CLICKHOUSE_URL", "http://127.0.0.1:18123/"))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, fmt.Errorf("CLICKHOUSE_URL must be an HTTP(S) endpoint")
	}
	return &Store{
		endpoint: endpoint,
		database: envOr("CLICKHOUSE_DB", "logs"),
		user:     envOr("CLICKHOUSE_USER", "log_ingester"),
		pass:     envOr("CLICKHOUSE_PASSWORD", "exercise-only"),
		client:   &http.Client{Timeout: 5 * time.Second},
	}, nil
}

func (s *Store) Init(ctx context.Context) error {
	return s.execute(ctx, schema, nil)
}

type row struct {
	Time    string `json:"event_time"`
	Level   string `json:"level"`
	Service string `json:"service"`
	Message string `json:"message"`
}

func (s *Store) Append(ctx context.Context, entries []logs.Entry) error {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, entry := range entries {
		if err := encoder.Encode(row{entry.Time.UTC().Format(timestampLayout), entry.Level, entry.Service, entry.Message}); err != nil {
			return err
		}
	}
	return s.execute(ctx, "INSERT INTO log_events FORMAT JSONEachRow", &body)
}

func (s *Store) Search(ctx context.Context, filter logs.Filter) ([]logs.Entry, error) {
	query := "SELECT event_time, level, service, message FROM log_events WHERE 1"
	args := url.Values{"param_limit": {strconv.Itoa(filter.Limit)}}
	if filter.Level != "" {
		query += " AND level = {level:String}"
		args.Set("param_level", filter.Level)
	}
	if filter.Service != "" {
		query += " AND service = {service:String}"
		args.Set("param_service", filter.Service)
	}
	if !filter.From.IsZero() {
		query += " AND event_time >= toDateTime64({from:String}, 6, 'UTC')"
		args.Set("param_from", filter.From.UTC().Format(timestampLayout))
	}
	if !filter.To.IsZero() {
		query += " AND event_time < toDateTime64({to:String}, 6, 'UTC')"
		args.Set("param_to", filter.To.UTC().Format(timestampLayout))
	}
	query += " ORDER BY event_time DESC, service, level, message LIMIT {limit:UInt16} FORMAT JSONEachRow"

	result, err := s.request(ctx, query, nil, args)
	if err != nil {
		return nil, err
	}
	defer result.Close()

	decoder := json.NewDecoder(result)
	var entries []logs.Entry
	for {
		var value row
		err := decoder.Decode(&value)
		if err == io.EOF {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}

		at, err := time.Parse(timestampLayout, value.Time)
		if err != nil {
			return nil, err
		}
		entries = append(entries, logs.Entry{Time: at, Level: value.Level, Service: value.Service, Message: value.Message})
	}
}

func (s *Store) execute(ctx context.Context, query string, body io.Reader) error {
	result, err := s.request(ctx, query, body, nil)
	if err != nil {
		return err
	}
	defer result.Close()
	detail, err := io.ReadAll(io.LimitReader(result, 4096))
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(detail)) != 0 {
		return fmt.Errorf("unexpected ClickHouse response: %s", strings.TrimSpace(string(detail)))
	}
	return nil
}

func (s *Store) request(ctx context.Context, query string, body io.Reader, args url.Values) (io.ReadCloser, error) {
	endpoint := *s.endpoint
	values := endpoint.Query()
	for name, value := range args {
		values[name] = value
	}
	values.Set("database", s.database)
	values.Set("query", query)
	values.Set("async_insert", "0")
	values.Set("wait_end_of_query", "1")
	endpoint.RawQuery = values.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	request.SetBasicAuth(s.user, s.pass)
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("ClickHouse HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	return response.Body, nil
}

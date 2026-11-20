//go:build integration

package clickhouse_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dudo/log-ingester/internal/clickhouse"
	"github.com/dudo/log-ingester/internal/httpapi"
	"github.com/dudo/log-ingester/internal/logs"
)

func TestClickHouseIngestAndSearch(t *testing.T) {
	store, err := clickhouse.OpenFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(t.Context()); err != nil {
		t.Fatal(err)
	}

	service := fmt.Sprintf("dispatch-test-%d", time.Now().UnixNano())
	from := "2026-03-04T09:13:01.245234Z"
	to := "2026-03-04T09:13:01.245235Z"
	payload := fmt.Sprintf("%s ERROR %s Lease  expired\n%s ERROR %s Later\n%s INFO %s Started\n%s ERROR %s-other Other service",
		from, service, to, service, from, service, from, service,
	)
	handler := httpapi.New(store, store)
	posted := httptest.NewRecorder()
	handler.ServeHTTP(posted, httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(payload)))
	if posted.Code != http.StatusAccepted {
		t.Fatalf("ingest status %d: %s", posted.Code, posted.Body.String())
	}

	filters := url.Values{"service": {service}, "level": {"ERROR"}, "from": {from}, "to": {to}}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/logs?"+filters.Encode(), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("search status %d: %s", response.Code, response.Body.String())
	}

	var entries []logs.Entry
	if err := json.NewDecoder(response.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Message != "Lease  expired" ||
		entries[0].Time.Format(time.RFC3339Nano) != from {
		t.Fatalf("search entries = %+v", entries)
	}
}

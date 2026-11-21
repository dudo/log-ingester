//go:build durability

package clickhouse_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dudo/log-ingester/internal/clickhouse"
	"github.com/dudo/log-ingester/internal/httpapi"
	"github.com/dudo/log-ingester/internal/logs"
)

type failFirstInsert struct {
	store  logs.Writer
	failed bool
}

func (s *failFirstInsert) Append(ctx context.Context, entries []logs.Entry) error {
	if !s.failed {
		s.failed = true
		return errors.New("synthetic ClickHouse insert rejection")
	}
	return s.store.Append(ctx, entries)
}

func TestAcknowledgedLogsSurviveInsertFailure(t *testing.T) {
	store, err := clickhouse.OpenFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(t.Context()); err != nil {
		t.Fatal(err)
	}

	fault := &failFirstInsert{store: store}
	failedRows := 0
	batch := logs.NewBatcher(fault, logs.BatchConfig{
		QueueDepth: 2, FlushAt: 32, FlushEvery: time.Hour,
	}, func(rows int, _ error) { failedRows += rows })
	t.Cleanup(batch.Close)

	service := fmt.Sprintf("durability-test-%d", time.Now().UnixNano())
	var response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(
		"2026-03-04T09:13:01.245234Z ERROR "+service+" Accepted before insert failure",
	))
	router := chi.NewRouter()
	httpapi.RegisterLogRoutes(router, batch, store)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("fixture got HTTP %d, want 202", response.Code)
	}

	batch.Close()
	if failedRows != 1 {
		t.Fatalf("fault injection affected %d rows, want 1", failedRows)
	}
	if err := fault.Append(t.Context(), []logs.Entry{{
		Time: time.Now().UTC(), Level: logs.LevelInfo, Service: service, Message: "Control insert after recovery",
	}}); err != nil {
		t.Fatalf("control insert: %v", err)
	}
	rows, err := store.Search(t.Context(), logs.Filter{Service: service, Level: logs.LevelError, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("acknowledged log missing after insert failure: HTTP 202, failed flush rows=%d, matching ClickHouse rows=%d; want 1", failedRows, len(rows))
	}
}

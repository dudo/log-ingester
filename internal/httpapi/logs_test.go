package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/dudo/log-ingester/internal/logs"
)

func newLogHandler(writer logs.Writer, reader logs.Reader) http.Handler {
	router := chi.NewRouter()
	RegisterLogRoutes(router, writer, reader)
	return router
}

type writerFunc func(context.Context, []logs.Entry) error

func (f writerFunc) Append(ctx context.Context, entries []logs.Entry) error {
	return f(ctx, entries)
}

const validLine = "2026-03-04T09:13:01.245234Z **ERROR** dispatch Lease  expired before pickup: 00000000-0000-4000-8000-000000000001 [response_time=592ms]"

func TestIngestPreservesTimestampAndMessage(t *testing.T) {
	var got []logs.Entry
	handler := newLogHandler(writerFunc(func(_ context.Context, entries []logs.Entry) error {
		got = entries
		return nil
	}), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(validLine+"\r\n")))

	if response.Code != http.StatusAccepted || len(got) != 1 {
		t.Fatalf("status=%d, entries=%v", response.Code, got)
	}
	entry := got[0]
	if entry.Time.Nanosecond() != 245234000 || entry.Level != logs.LevelError ||
		entry.Service != "dispatch" || entry.Message != "Lease  expired before pickup: 00000000-0000-4000-8000-000000000001 [response_time=592ms]" {
		t.Fatalf("parsed entry = %+v", entry)
	}
}

func TestIngestRejectsMalformedOrOversizedRequestsBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"malformed second line", validLine + "\nbad input", http.StatusBadRequest},
		{"unknown level", strings.Replace(validLine, "**ERROR**", "unknown", 1), http.StatusBadRequest},
		{"empty", " \n", http.StatusBadRequest},
		{"oversized body", strings.Repeat("x", maxBodyBytes+1), http.StatusRequestEntityTooLarge},
		{"oversized line", validLine + strings.Repeat("x", maxLineBytes), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := newLogHandler(writerFunc(func(context.Context, []logs.Entry) error {
				t.Fatal("invalid request reached the writer")
				return nil
			}), nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(tc.body)))
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
		})
	}
}

func TestIngestReturns503WhenStoreFails(t *testing.T) {
	handler := newLogHandler(writerFunc(func(context.Context, []logs.Entry) error {
		return errors.New("synthetic insert failure")
	}), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(validLine)))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestSearchRejectsInvalidFilters(t *testing.T) {
	for _, query := range []string{"level=unknown", "from=yesterday", "limit=501", "from=2026-03-05T00:00:00Z&to=2026-03-04T00:00:00Z"} {
		response := httptest.NewRecorder()
		newLogHandler(nil, nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/logs?"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", query, response.Code)
		}
	}
}

func TestLogRouteMethods(t *testing.T) {
	handler := newLogHandler(nil, nil)
	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/v1/logs?limit=0", nil))
	if head.Code != http.StatusBadRequest {
		t.Fatalf("HEAD status = %d, want search handler to reject invalid limit with 400", head.Code)
	}

	put := httptest.NewRecorder()
	handler.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/v1/logs", nil))
	if put.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status = %d, want 405", put.Code)
	}
	allowed := strings.Fields(strings.ReplaceAll(strings.Join(put.Header().Values("Allow"), " "), ",", " "))
	slices.Sort(allowed)
	if !slices.Equal(allowed, []string{http.MethodGet, http.MethodHead, http.MethodPost}) {
		t.Fatalf("Allow = %v, want GET, HEAD, POST", allowed)
	}
}

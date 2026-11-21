package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/dudo/log-ingester/internal/logs"
)

const maxBodyBytes = 256 << 10
const maxLineBytes = 8 << 10

func RegisterLogRoutes(router chi.Router, writer logs.Writer, reader logs.Reader) {
	const logsPath = "/v1/logs"
	searchLogs := search(reader)
	router.Post(logsPath, ingest(writer))
	router.Get(logsPath, searchLogs)
	router.Head(logsPath, searchLogs)
}

func ingest(writer logs.Writer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			var sizeErr *http.MaxBytesError
			if errors.As(err, &sizeErr) {
				http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "could not read request", http.StatusBadRequest)
			return
		}

		var entries []logs.Entry
		for i, line := range bytes.Split(body, []byte{'\n'}) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			if len(line) > maxLineBytes {
				http.Error(w, fmt.Sprintf("line %d too long", i+1), http.StatusBadRequest)
				return
			}
			entry, err := logs.ParseLine(string(line))
			if err != nil {
				http.Error(w, fmt.Sprintf("line %d: %s", i+1, err), http.StatusBadRequest)
				return
			}
			entries = append(entries, entry)
		}
		if len(entries) == 0 {
			http.Error(w, "expected at least one log line", http.StatusBadRequest)
			return
		}

		if err := writer.Append(r.Context(), entries); err != nil {
			http.Error(w, "logs not accepted", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func search(reader logs.Reader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter, err := readFilter(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		entries, err := reader.Search(r.Context(), filter)
		if err != nil {
			http.Error(w, "log search unavailable", http.StatusServiceUnavailable)
			return
		}
		if entries == nil {
			entries = []logs.Entry{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
	}
}

func readFilter(r *http.Request) (logs.Filter, error) {
	query := r.URL.Query()
	filter := logs.Filter{Service: strings.TrimSpace(query.Get("service")), Limit: 100}
	var err error
	if level := query.Get("level"); level != "" {
		filter.Level, err = logs.ParseLevel(level)
		if err != nil {
			return filter, err
		}
	}
	if from := query.Get("from"); from != "" {
		filter.From, err = logs.ParseTime(from)
		if err != nil {
			return filter, fmt.Errorf("invalid from: %w", err)
		}
	}
	if to := query.Get("to"); to != "" {
		filter.To, err = logs.ParseTime(to)
		if err != nil {
			return filter, fmt.Errorf("invalid to: %w", err)
		}
	}
	if !filter.From.IsZero() && !filter.To.IsZero() && !filter.From.Before(filter.To) {
		return filter, fmt.Errorf("from must be earlier than to")
	}
	if limit := query.Get("limit"); limit != "" {
		filter.Limit, err = strconv.Atoi(limit)
		if err != nil || filter.Limit < 1 || filter.Limit > 500 {
			return filter, fmt.Errorf("limit must be between 1 and 500")
		}
	}
	return filter, nil
}

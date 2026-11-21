package logs

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
)

type Level string

const (
	LevelDebug   Level = "DEBUG"
	LevelInfo    Level = "INFO"
	LevelWarning Level = "WARNING"
	LevelError   Level = "ERROR"
)

type Entry struct {
	Time    time.Time `json:"timestamp"`
	Level   Level     `json:"level"`
	Service string    `json:"service"`
	Message string    `json:"message"`
}

type Filter struct {
	Level    Level
	Service  string
	From, To time.Time
	Limit    int
}

type Writer interface {
	Append(context.Context, []Entry) error
}

type Reader interface {
	Search(context.Context, Filter) ([]Entry, error)
}

func ParseLevel(value string) (Level, error) {
	level := Level(value)
	switch level {
	case LevelDebug, LevelInfo, LevelWarning, LevelError:
		return level, nil
	default:
		return "", fmt.Errorf("invalid level")
	}
}

func ParseTime(value string) (time.Time, error) {
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || at.Nanosecond()%1000 != 0 {
		return time.Time{}, fmt.Errorf("expected RFC3339 timestamp with at most microsecond precision")
	}
	at = at.UTC()
	if at.Year() < 1900 || at.Year() > 2299 {
		return time.Time{}, fmt.Errorf("timestamp is outside the supported range")
	}
	return at, nil
}

func ParseLine(line string) (Entry, error) {
	fields := strings.SplitN(strings.TrimSuffix(line, "\r"), " ", 4)
	if len(fields) != 4 {
		return Entry{}, fmt.Errorf("expected a timestamp, level, service and message")
	}
	rawLevel := fields[1]
	if len(rawLevel) > 4 && strings.HasPrefix(rawLevel, "**") && strings.HasSuffix(rawLevel, "**") {
		rawLevel = rawLevel[2 : len(rawLevel)-2]
	}
	level, err := ParseLevel(rawLevel)
	if err != nil ||
		fields[2] == "" || strings.IndexFunc(fields[2], unicode.IsSpace) >= 0 ||
		strings.TrimSpace(fields[3]) == "" {
		return Entry{}, fmt.Errorf("expected a timestamp, level, service and message")
	}

	at, err := ParseTime(fields[0])
	if err != nil {
		return Entry{}, err
	}
	return Entry{Time: at, Level: level, Service: fields[2], Message: fields[3]}, nil
}

CREATE TABLE IF NOT EXISTS log_events
(
    event_time DateTime64(6, 'UTC'),
    level LowCardinality(String),
    service LowCardinality(String),
    message String
)
ENGINE = MergeTree
ORDER BY (service, event_time)

FROM golang:1.27.4-bookworm AS base

ENV TINI_KILL_PROCESS_GROUP=1

WORKDIR /usr/src/app
COPY go.mod go.sum ./
RUN go mod download

FROM base AS build
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -o /out/log-ingester ./cmd/log-ingester

FROM alpine:3.23
COPY --from=build /out/log-ingester /usr/local/bin/log-ingester
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/log-ingester"]

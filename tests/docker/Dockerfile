# Portable test image: bundles the Go functional suite (cases 1-4), the
# report-gen HTML generator, and the k6 load/stress scripts (cases 5-6).
#
# Build (from repo root):
#   docker build -f tests/docker/Dockerfile -t webhook-tests:local .
#
# Run functional cases against staging, write HTML report to ./out:
#   docker run --rm -v "$PWD/out:/reports" \
#     -e E2E_EVENT_RECEIVER_URL=http://staging.dws.sidey383.ru \
#     -e E2E_SUBSCRIPTIONS_URL=http://subscriptions.dws.sidey383.ru \
#     -e E2E_BASIC_AUTH_USER=admin -e E2E_BASIC_AUTH_PASS=*** \
#     -e E2E_CALLBACK_HOST=<reachable-host> -e E2E_CALLBACK_PORT=8089 \
#     -p 8089:8089 \
#     webhook-tests:local functional
#
# Run load test:
#   docker run --rm -v "$PWD/out:/reports" \
#     -e E2E_EVENT_RECEIVER_URL=... -e E2E_LOAD_RPS=200 -e E2E_LOAD_DURATION=10m \
#     webhook-tests:local load

ARG GO_VERSION=1.26.2

# --- k6 binary source ------------------------------------------------------
FROM grafana/k6:latest AS k6

# --- builder: compile report-gen and warm the module cache -----------------
FROM golang:${GO_VERSION} AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/report-gen ./cmd/report-gen
# Pre-compile the test binary so `go test` is fast and offline at run time.
RUN CGO_ENABLED=0 go test -c -o /out/suite.test ./tests/suite

# --- final image -----------------------------------------------------------
FROM golang:${GO_VERSION}
WORKDIR /app

# k6 for load/stress
COPY --from=k6 /usr/bin/k6 /usr/local/bin/k6
# report generator + precompiled functional test binary
COPY --from=builder /out/report-gen /usr/local/bin/report-gen
COPY --from=builder /out/suite.test /usr/local/bin/suite.test
# source (test binary needs nothing at runtime, but k6 scripts + go source for
# the optional `go test` path are handy and small)
COPY . .

RUN mkdir -p /reports
VOLUME ["/reports"]

ENV E2E_K6_REPORT_HTML=/reports/k6-report.html \
    E2E_K6_REPORT_JSON=/reports/k6-summary.json

COPY tests/docker/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["functional"]

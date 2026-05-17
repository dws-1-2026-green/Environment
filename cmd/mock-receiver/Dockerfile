FROM golang:alpine AS builder
WORKDIR /app
COPY main.go .
RUN go mod init receiver && CGO_ENABLED=0 GOOS=linux go build -o /mock-receiver .

FROM alpine:latest
COPY --from=builder /mock-receiver /mock-receiver
ENTRYPOINT ["/mock-receiver"]

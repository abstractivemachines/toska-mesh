FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/gateway    ./cmd/gateway
RUN CGO_ENABLED=0 go build -o /bin/discovery   ./cmd/discovery
RUN CGO_ENABLED=0 go build -o /bin/healthmonitor ./cmd/healthmonitor

FROM alpine:3.21 AS gateway
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/gateway /usr/local/bin/gateway
ENTRYPOINT ["gateway"]

FROM alpine:3.21 AS discovery
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/discovery /usr/local/bin/discovery
ENTRYPOINT ["discovery"]

FROM alpine:3.21 AS healthmonitor
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/healthmonitor /usr/local/bin/healthmonitor
ENTRYPOINT ["healthmonitor"]

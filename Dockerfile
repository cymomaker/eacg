FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/eacg-example \
    ./cmd/eacg-example

FROM alpine:3.22

RUN addgroup -S eacg && adduser -S -G eacg eacg
WORKDIR /app
COPY --from=builder /out/eacg-example /app/eacg-example

ENV EACG_ADDRESS=0.0.0.0:8080
EXPOSE 8080

USER eacg
ENTRYPOINT ["/app/eacg-example"]

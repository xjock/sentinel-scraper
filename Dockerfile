FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true

COPY . .
RUN go build -ldflags="-s -w" -o sentinel-scraper main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/sentinel-scraper .

ENTRYPOINT ["./sentinel-scraper"]

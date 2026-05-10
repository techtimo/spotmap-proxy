FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o spotmap-proxy .

FROM alpine:3.20
# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates tzdata sqlite
WORKDIR /app
COPY --from=builder /app/spotmap-proxy .
EXPOSE 8080
VOLUME ["/data"]
CMD ["./spotmap-proxy"]

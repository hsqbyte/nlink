FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/nlink main.go

# ---

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1000 nlink
ENV TZ=Asia/Shanghai

WORKDIR /app

COPY --from=builder /app/nlink .
COPY config/ ./config/

USER nlink

EXPOSE 7000 7001 18080

HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD wget -qO- http://localhost:18080/health || exit 1

CMD ["./nlink", "-c", "config/nlink.yaml"]

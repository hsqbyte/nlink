FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/server main.go

# ---

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai

WORKDIR /app

COPY --from=builder /app/server .
COPY config/ ./config/
COPY docs/db/ ./docs/db/
COPY docs/swagger/ ./docs/swagger/
COPY templates/ ./templates/

EXPOSE 18081

CMD ["./server"]

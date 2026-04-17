# syntax=docker/dockerfile:1.7

# ---------- 构建阶段 ----------
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

# 利用层缓存：先拉依赖
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 再拷源码
COPY . .

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/nlink .

# ---------- 运行阶段 ----------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -S nlink && adduser -S -G nlink -u 10001 nlink

ENV TZ=Asia/Shanghai

WORKDIR /app

COPY --from=builder /out/nlink /app/nlink
COPY config/ /app/config/

# 数据目录（运行时写日志）
RUN mkdir -p /app/data && chown -R nlink:nlink /app

# 注意：TUN/VPN 需要 NET_ADMIN + /dev/net/tun，使用 VPN 时在 compose 里追加 cap_add
USER nlink

EXPOSE 7000 7001 18080 7200/udp

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:18080/health || exit 1

ENTRYPOINT ["/app/nlink"]
CMD ["-c", "/app/config/nlink.yaml"]

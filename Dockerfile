# syntax=docker/dockerfile:1.7

FROM node:24-alpine AS web-builder
WORKDIR /src/web

RUN corepack enable && corepack prepare pnpm@10.30.2 --activate
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
	pnpm install --frozen-lockfile

COPY web/ ./
RUN pnpm build:sdk && pnpm build

FROM golang:1.26-alpine AS server-builder
WORKDIR /src

RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
	go mod download

COPY . ./
COPY --from=web-builder /src/web/out ./web/out

ARG TARGETOS=linux
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
	go build -v -trimpath -ldflags="-s -w" -o /out/agent-desk ./cmd/server

FROM alpine:3.22 AS app
WORKDIR /app

ENV TZ=Asia/Shanghai

RUN apk add --no-cache ca-certificates tzdata wget \
	&& mkdir -p /app/config /app/data/storage

COPY --from=server-builder /out/agent-desk /app/agent-desk
COPY config/config.example.yaml /app/config/config.example.yaml
COPY config/config.example.yaml /app/config/config.yaml

EXPOSE 8083
VOLUME ["/app/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
	CMD wget -qO- http://127.0.0.1:8083/ >/dev/null || exit 1

CMD ["/app/agent-desk", "-config", "/app/config/config.yaml"]

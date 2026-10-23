FROM node:22-alpine AS ui
WORKDIR /build/ui
COPY ui/package*.json ./
RUN npm ci --no-audit --no-fund
COPY ui/ ./
RUN npm run build

FROM golang:1.24-alpine AS server
WORKDIR /build
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download -x
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=ui /build/internal/web/static/ ./internal/web/static/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /shiying ./cmd/shiying

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S -g 10001 shiying && adduser -S -u 10001 -G shiying shiying && mkdir -p /data && chown shiying:shiying /data
COPY --from=server /shiying /usr/local/bin/shiying
COPY LICENSE NOTICE /usr/share/licenses/shiying/
ENV SHIYING_ADDR=0.0.0.0:8080 SHIYING_DATA_DIR=/data
USER shiying
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s CMD wget -q -O - http://127.0.0.1:8080/api/health || exit 1
ENTRYPOINT ["shiying"]

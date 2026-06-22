# syntax=docker/dockerfile:1
#
# Build context is the repo root (see docker-compose.yml) so this stage can
# pull in both api/ (the Go module) and web/ (htmx UI + Go templates served at
# runtime). Build:  docker compose build   /   Run: docker compose up -d

# ---- build -----------------------------------------------------------------
FROM golang:1.26-bookworm AS build
WORKDIR /src/api

# Cached dependency layer — only re-runs when go.mod/go.sum change.
COPY api/go.mod api/go.sum ./
RUN go mod download

COPY api/ ./
# Pure-Go (pgx, no CGO) → fully static binaries, runnable on a scratch image.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server  ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

# ---- runtime ---------------------------------------------------------------
# distroless/static ships ca-certificates (needed for the DeepSeek HTTPS API)
# and tzdata, nothing else. Runs as an unprivileged user.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build /out/server  /app/server
COPY --from=build /out/migrate /app/migrate
COPY web/        /app/web/
COPY migrations/ /app/migrations/

# WEB_DIR points the static file server + template loader at the baked-in UI.
# HTTP_ADDR can be overridden by compose; :7770 is the project's chosen port.
ENV WEB_DIR=/app/web \
    HTTP_ADDR=:7770

EXPOSE 7770
USER nonroot:nonroot
ENTRYPOINT ["/app/server"]

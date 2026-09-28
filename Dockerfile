# syntax=docker/dockerfile:1

# ---- stage 1: build the web SPA ----
FROM node:24-alpine AS web
WORKDIR /web
RUN corepack enable
# pnpm-workspace.yaml carries allowBuilds (esbuild's install script); the
# pnpm version is pinned by package.json "packageManager".
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
# vite.config.ts writes to ../internal/webui/dist for local builds; inside
# the image build into ./dist, which the Go stage copies into place.
RUN pnpm exec tsc -b && pnpm exec vite build --outDir dist --emptyOutDir

# ---- stage 2: build the Go gateway ----
FROM golang:1.26-alpine AS build
WORKDIR /src
# goproxy.cn suits builds inside mainland China; overseas hosts can pass
# --build-arg GOPROXY=https://proxy.golang.org,direct
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# bring in the built SPA so go:embed picks it up
COPY --from=web /web/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

# ---- stage 3: runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/gateway /app/gateway
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/gateway"]
CMD ["serve"]

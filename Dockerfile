# pond ships as one statically-linked binary with the SPA and Vue runtime
# embedded (see cmd/pond/serve.go), so the production image is the binary and
# nothing else: no Go toolchain, no asset directory, no CDN, no libc. The build
# stage compiles it; the final stage is `scratch`.
FROM golang:1.26-alpine AS build
WORKDIR /src
# Resolve modules before copying source so the dependency layer caches across
# code-only changes.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO off → a fully static binary that runs on scratch. -trimpath/-s -w keep the
# image to just the code.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/pond ./cmd/pond

FROM scratch
COPY --from=build /out/pond /pond
# The FileStore writes decision files here; mount a volume to persist them.
WORKDIR /data
EXPOSE 8080
# Serve the embedded web UI + API on :8080, scoped to the single local demo
# owner. Append flags to override (e.g. --owner, --addr).
ENTRYPOINT ["/pond", "serve", "--dir", "/data", "--addr", ":8080"]

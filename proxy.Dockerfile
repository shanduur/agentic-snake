# Copyright 2026 Mateusz Urbanek.
FROM golang:1.26.8@sha256:0f063af2d465d8dcae54cce04278ada488b96f77b42449c8d071e47d016cc65a AS build
WORKDIR /build
COPY proxy/go.mod proxy/go.sum ./
RUN go mod download
COPY proxy/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mcp-proxy ./cmd/mcp-proxy

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
COPY --from=build /mcp-proxy /mcp-proxy
USER 65532:65532
ENTRYPOINT ["/mcp-proxy"]

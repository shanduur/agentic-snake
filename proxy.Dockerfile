# Copyright 2026 Mateusz Urbanek.
FROM golang:1.26.2@sha256:b54cbf583d390341599d7bcbc062425c081105cc5ef6d170ced98ef9d047c716 AS build
WORKDIR /build
COPY proxy/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mcp-proxy ./cmd/mcp-proxy

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
COPY --from=build /mcp-proxy /mcp-proxy
USER 65532:65532
ENTRYPOINT ["/mcp-proxy"]

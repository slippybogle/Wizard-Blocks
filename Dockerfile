FROM --platform=$BUILDPLATFORM golang:1.24.7-bookworm@sha256:b8bae5bd9ba9b1f89b635c91c24cc75cea335a16fb5076310f38566fc674b1ec AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
# Static binary: no cgo; one dependency (golang.org/x/crypto, for scrypt).
ARG TARGETOS TARGETARCH
RUN mkdir -p /out/data && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/wizard-blocks ./cmd/wizard-blocks

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/wizard-blocks /usr/local/bin/wizard-blocks
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
# Stratum, web UI, stats API
EXPOSE 62023 51492 8420 8080
ENV WB_STRATUM_LISTEN=0.0.0.0:62023 \
    WB_UI_LISTEN=0.0.0.0:8420 \
    WB_API_LISTEN=0.0.0.0:8080 \
    WB_DATA_DIR=/data
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/wizard-blocks"]

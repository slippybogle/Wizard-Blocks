# syntax=docker/dockerfile:1
FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
# Static binary: no cgo, standard library only.
RUN mkdir -p /out/data && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/wizard-blocks ./cmd/wizard-blocks

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/wizard-blocks /usr/local/bin/wizard-blocks
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
# Stratum, stats API
EXPOSE 3333 8080
ENV WB_STRATUM_LISTEN=0.0.0.0:3333 \
    WB_API_LISTEN=0.0.0.0:8080 \
    WB_DATA_DIR=/data
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/wizard-blocks"]

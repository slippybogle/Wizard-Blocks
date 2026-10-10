# syntax=docker/dockerfile:1
# Node Status page (cmd/nodestatus): standard library only, static binary.
# Build context: the repository root.
FROM --platform=$BUILDPLATFORM golang:1.24.7-bookworm@sha256:b8bae5bd9ba9b1f89b635c91c24cc75cea335a16fb5076310f38566fc674b1ec AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/nodestatus ./cmd/nodestatus

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/nodestatus /usr/local/bin/nodestatus
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/nodestatus"]

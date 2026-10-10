# syntax=docker/dockerfile:1
# Dogecoin Core v1.14.9 for Umbrel, from the official release binaries.
# The build refuses to continue unless SHA256SUMS.asc carries a good
# signature from the pinned key (patricklodder.asc, fingerprint DC6EF4A8BF9F1B1E4DE1EE522D3A345B98D0DC1F)
# and the tarball for this platform matches the signed checksum.
# Runtime base, pinned by digest (docker.io and mirror.gcr.io serve the same).
ARG BASE=debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
ARG VERSION=1.14.9

# Verify stage (runs on the build machine; it only picks the tarball for
# TARGETARCH): a Debian image that already has gpg and sha256sum
# (golang:1.24.7-bookworm, pinned by digest).
FROM --platform=$BUILDPLATFORM golang:1.24.7-bookworm@sha256:b8bae5bd9ba9b1f89b635c91c24cc75cea335a16fb5076310f38566fc674b1ec AS verify
ARG VERSION
ARG TARGETARCH
ADD --checksum=sha256:070673a1c9fdf271829fefb4a794c6b040e3275a319d2ae4ed2959298406020f https://github.com/dogecoin/dogecoin/releases/download/v${VERSION}/SHA256SUMS.asc /rel/SHA256SUMS.asc
ADD https://github.com/dogecoin/dogecoin/releases/download/v${VERSION}/dogecoin-${VERSION}-x86_64-linux-gnu.tar.gz /rel/
ADD https://github.com/dogecoin/dogecoin/releases/download/v${VERSION}/dogecoin-${VERSION}-aarch64-linux-gnu.tar.gz /rel/
COPY patricklodder.asc /rel/key.asc
RUN set -eu; cd /rel; \
    export GNUPGHOME=/rel/gnupg; mkdir -m 700 $GNUPGHOME; \
    gpg --batch --import key.asc; \
    gpg --batch --status-fd 1 --verify SHA256SUMS.asc 2>/dev/null | grep -q "^\[GNUPG:\] VALIDSIG DC6EF4A8BF9F1B1E4DE1EE522D3A345B98D0DC1F "; \
    case "$TARGETARCH" in amd64) t=x86_64-linux-gnu ;; arm64) t=aarch64-linux-gnu ;; *) echo "unsupported $TARGETARCH"; exit 1 ;; esac; \
    f=dogecoin-${VERSION}-$t.tar.gz; \
    gpg --batch --decrypt SHA256SUMS.asc 2>/dev/null | grep " $f$" | sha256sum -c -; \
    tar -xzf "$f"; mkdir /out; \
    install -m 0755 dogecoin-${VERSION}/bin/dogecoind dogecoin-${VERSION}/bin/dogecoin-cli /out/

FROM ${BASE}
COPY --from=verify /out/ /usr/local/bin/
RUN useradd --uid 1000 --create-home --home-dir /home/node node && mkdir /data && chown 1000:1000 /data
USER 1000:1000
VOLUME /data
ENTRYPOINT ["dogecoind"]

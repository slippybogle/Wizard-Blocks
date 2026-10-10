# Dogecoin Core for the regtests, from the official release binaries.
# The tarball is downloaded by the harness and must match the SHA-256 in
# the release's SHA256SUMS.asc, signed by Patrick Lodder (key
# DC6EF4A8BF9F1B1E4DE1EE522D3A345B98D0DC1F); it is checked again here.
ARG BASE=mirror.gcr.io/library/debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
FROM ${BASE}
ARG DOGE_VERSION=1.14.9
ARG DOGE_SHA256=4f227117b411a7c98622c970986e27bcfc3f547a72bef65e7d9e82989175d4f8
COPY dogecoin-${DOGE_VERSION}-x86_64-linux-gnu.tar.gz /tmp/doge.tar.gz
RUN echo "${DOGE_SHA256}  /tmp/doge.tar.gz" | sha256sum -c - \
 && tar -xzf /tmp/doge.tar.gz -C /tmp \
 && install -m 0755 /tmp/dogecoin-${DOGE_VERSION}/bin/dogecoind /tmp/dogecoin-${DOGE_VERSION}/bin/dogecoin-cli /usr/local/bin/ \
 && rm -rf /tmp/doge.tar.gz /tmp/dogecoin-${DOGE_VERSION}
ENTRYPOINT ["dogecoind"]

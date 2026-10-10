# Litecoin Core for the regtests, from the official release binaries.
# The tarball is downloaded by the harness and must match the SHA-256 in
# the release's SHA256SUMS.asc, signed by David Burkett (key
# D35621D53A1CC6A3456758D03620E9D387E55666); it is checked again here.
# Base: debian:bookworm-slim pinned by digest (docker.io and mirror.gcr.io
# serve the same digest).
ARG BASE=mirror.gcr.io/library/debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
FROM ${BASE}
ARG LTC_VERSION=0.21.5.8
ARG LTC_SHA256=43200c9f9d65ebc126ea5833ca9429e144c4b3273da6bb9f4e89fd7450ab1be9
COPY litecoin-${LTC_VERSION}-x86_64-linux-gnu.tar.gz /tmp/ltc.tar.gz
RUN echo "${LTC_SHA256}  /tmp/ltc.tar.gz" | sha256sum -c - \
 && tar -xzf /tmp/ltc.tar.gz -C /tmp \
 && install -m 0755 /tmp/litecoin-${LTC_VERSION}/bin/litecoind /tmp/litecoin-${LTC_VERSION}/bin/litecoin-cli /usr/local/bin/ \
 && rm -rf /tmp/ltc.tar.gz /tmp/litecoin-${LTC_VERSION}
ENTRYPOINT ["litecoind"]

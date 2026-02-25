# STEP1: CLONE THE CODE
FROM alpine/git as builder_prepare
WORKDIR /home/
ARG GIT_REPO=https://github.com/zemzema/filestash_dwg
ARG GIT_BRANCH=master
RUN git clone --depth 1 --single-branch --branch ${GIT_BRANCH} ${GIT_REPO} filestash

# STEP2: BUILD LIBREDWG
FROM debian:stable-slim AS builder_libredwg
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    git \
    autoconf \
    automake \
    libtool \
    gettext \
    pkg-config \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build
RUN git clone --depth=1 https://github.com/LibreDWG/libredwg.git && \
    cd libredwg && \
    autoreconf -vif && \
    ./configure --disable-docs && \
    make -j$(nproc) && \
    make install DESTDIR=/libredwg-dist

# STEP3: BUILD BACKEND
FROM golang:1.24-trixie AS builder_backend
WORKDIR /home/filestash/
COPY --from=builder_prepare /home/filestash/ .
RUN apt-get update > /dev/null && \
    apt-get install -y curl make > /dev/null 2>&1 && \
    apt-get install -y libjpeg-dev libtiff-dev libpng-dev libwebp-dev libraw-dev libheif-dev libgif-dev libvips-dev > /dev/null 2>&1 && \
    make build_init && \
    make build_backend && \
    mkdir -p ./dist/data/state/config/ && \
    cp config/config.json ./dist/data/state/config/config.json

# STEP4: BUILD PROD IMAGE
FROM debian:stable-slim
MAINTAINER mickael@kerjean.me
WORKDIR /app/
COPY --from=builder_backend /home/filestash/dist/ .
COPY --from=builder_libredwg /libredwg-dist/usr/local/bin/dwg2svg /usr/local/bin/dwg2svg
COPY --from=builder_libredwg /libredwg-dist/usr/local/lib/ /usr/local/lib/
RUN apt-get update > /dev/null && \
    apt-get install -y --no-install-recommends apt-utils && \
    apt-get install -y curl ffmpeg libjpeg-dev libtiff-dev libpng-dev libwebp-dev libraw-dev libheif-dev libgif-dev && \
    ldconfig && \
    useradd filestash && \
    chown -R filestash:filestash /app/ && \
    find /app/data/ -type d -exec chmod 770 {} \; && \
    find /app/data/ -type f -exec chmod 760 {} \; && \
    chmod 730 /app/filestash && \
    rm -rf /var/lib/apt/lists/* && \
    rm -rf /tmp/*
USER filestash
CMD ["/app/filestash"]
EXPOSE 8334

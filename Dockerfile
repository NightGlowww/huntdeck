# syntax=docker/dockerfile:1

# The Go build runs on the builder's own platform and cross-compiles for
# the target (no emulation; release images: make release-image).
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine3.24@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
# A mirror for networks where proxy.golang.org is slow or blocked:
#   docker compose build --build-arg GOPROXY=https://goproxy.cn,direct
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags "-s -w -X github.com/mdpsec/huntdeck/internal/version.Version=${VERSION}" \
      -o /out/huntdeck ./cmd/huntdeck \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags "-s -w -X github.com/mdpsec/huntdeck/internal/version.Version=${VERSION}" \
      -o /out/huntdeck-mcp ./cmd/huntdeck-mcp \
 && mkdir -p /out/data

# noVNC, to watch the login browsers when HUNTDECK_VNC is on: a pinned
# release, checked, served by HuntDeck at /novnc/ (no CDN, no websockify).
ARG NOVNC_VERSION=1.6.0
ARG NOVNC_SHA256=5066103959ef4e9b10f37e5a148627360dd8414e4cf8a7db92bdbd022e728aaa
# WITH_NOVNC=0 builds without the browser viewer (HUNTDECK_VNC): useful on
# networks where GitHub is unreachable. Everything else is unaffected.
ARG WITH_NOVNC=1
RUN if [ "$WITH_NOVNC" = "1" ]; then \
      wget -qO /tmp/novnc.tgz "https://github.com/novnc/noVNC/archive/refs/tags/v${NOVNC_VERSION}.tar.gz" \
   && echo "${NOVNC_SHA256}  /tmp/novnc.tgz" | sha256sum -c - \
   && mkdir -p /tmp/novnc /out/novnc && tar -xzf /tmp/novnc.tgz -C /tmp/novnc \
   && cd "/tmp/novnc/noVNC-${NOVNC_VERSION}" \
   && cp -r app core vendor vnc.html package.json defaults.json mandatory.json LICENSE.txt /out/novnc/ \
   && rm -rf /tmp/novnc /tmp/novnc.tgz; \
    else mkdir -p /out/novnc && echo "noVNC skipped (WITH_NOVNC=0)"; fi

# Debian with Google Chrome rather than distroless: website logins
# (HackerOne's and Intigriti's, for what their APIs leave out) need a real
# browser. HuntDeck runs one normal, windowed Chrome on a virtual display
# (Xvfb), a tab per platform, only when H1_EMAIL or IT_EMAIL is set. Google
# publishes Chrome for amd64 only; other architectures get Debian's
# Chromium. x11vnc shows that display (Settings > Browser) when HUNTDECK_VNC
# is on.
FROM debian:13-slim@sha256:89c1ea91e897f0fe0a3731595330bc45b8bdef4047464a07116f891a94054b31
ARG TARGETARCH
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata curl xvfb xauth x11vnc \
      fonts-liberation fonts-dejavu-core fonts-noto-color-emoji fonts-noto-cjk \
 && if [ "${TARGETARCH:-amd64}" = amd64 ]; then \
      curl -fsSLo /tmp/chrome.deb https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb \
      && apt-get install -y --no-install-recommends /tmp/chrome.deb && rm /tmp/chrome.deb; \
    else apt-get install -y --no-install-recommends chromium; fi \
 && fc-cache -f \
 && apt-get purge -y curl && apt-get autoremove -y && rm -rf /var/lib/apt/lists/* \
 && useradd --uid 65532 --no-create-home --shell /usr/sbin/nologin nonroot
# The MCP server rides along: `docker exec -i <container> /huntdeck-mcp`
# reaches the same instance over localhost (docs/mcp.md).
COPY --from=build /out/huntdeck /huntdeck
COPY --from=build /out/huntdeck-mcp /huntdeck-mcp
COPY --from=build /out/novnc /usr/share/novnc
# A new named volume inherits this ownership (and owner-only mode), so the
# nonroot user can write it and no one else can read it.
COPY --from=build --chown=65532:65532 --chmod=700 /out/data /data
# Chrome writes its caches under /tmp (a tmpfs: the root filesystem is
# read-only); its profile is kept in /data.
ENV HUNTDECK_DATA_DIR=/data \
    HUNTDECK_LISTEN=0.0.0.0:8080 \
    HOME=/tmp
USER 65532:65532
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/huntdeck", "healthcheck"]
ENTRYPOINT ["/huntdeck"]
CMD ["serve"]

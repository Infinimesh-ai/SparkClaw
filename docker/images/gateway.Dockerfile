FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.work ./
COPY services/gateway/go.mod services/gateway/go.mod
COPY services/gateway services/gateway
RUN go build -o /out/sparkclaw ./services/gateway/cmd/sparkclaw

FROM node:24-bookworm-slim
WORKDIR /app
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates chromium ffmpeg xvfb \
    && rm -rf /var/lib/apt/lists/*
COPY package.json package-lock.json ./
COPY apps/webchat/package.json apps/webchat/package.json
ENV HOME=/opt/agent-browser
RUN npm ci --omit=dev \
    && ./node_modules/.bin/agent-browser --version \
    && useradd --create-home --uid 10001 sparkclaw \
    && mkdir -p /opt/agent-browser/.agent-browser /var/lib/sparkclaw/browser-profiles \
    && chown -R sparkclaw:sparkclaw /opt/agent-browser/.agent-browser /var/lib/sparkclaw/browser-profiles \
    && chmod -R a+rwX /opt/agent-browser/.agent-browser /var/lib/sparkclaw/browser-profiles
COPY --from=build /out/sparkclaw /usr/local/bin/sparkclaw
COPY configs /app/configs
ENV SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE=/usr/bin/chromium
ENV SPARKCLAW_BROWSER_PROFILE_DIR=/var/lib/sparkclaw/browser-profiles
USER sparkclaw
EXPOSE 18789
ENTRYPOINT ["sparkclaw"]
CMD ["-config", "/app/configs/sparkclaw.default.json"]

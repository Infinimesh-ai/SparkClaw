FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.work ./
COPY services/gateway/go.mod services/gateway/go.mod
COPY services/gateway services/gateway
RUN go build -o /out/sparkclaw ./services/gateway/cmd/sparkclaw \
    && go build -o /out/iscp-bridge ./services/gateway/cmd/iscp-bridge

FROM node:26-bookworm-slim
WORKDIR /app
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/*
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 python3-pip \
    && rm -rf /var/lib/apt/lists/*
COPY package.json package-lock.json ./
COPY apps/webchat/package.json apps/webchat/package.json
COPY tools/document-runtime/package.json tools/document-runtime/package.json
COPY tools/document-runtime/requirements.txt tools/document-runtime/requirements.txt
RUN npm ci --omit=dev \
    && python3 -m pip install --break-system-packages --no-cache-dir --quiet -r tools/document-runtime/requirements.txt \
    && useradd --create-home --uid 10001 sparkclaw
COPY --from=build /out/sparkclaw /usr/local/bin/sparkclaw
COPY --from=build /out/iscp-bridge /usr/local/bin/iscp-bridge
COPY configs /app/configs
COPY scripts/browser_controller_smoke.mjs /app/scripts/browser_controller_smoke.mjs
COPY scripts/email /app/scripts/email
RUN chmod -R a+rX /app/configs /app/scripts
ENV SPARKCLAW_MODEL_CAPACITY_CATALOG=/app/configs/model.profiles.json
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8
USER sparkclaw
EXPOSE 18789
ENTRYPOINT ["sparkclaw"]
CMD ["-config", "/app/configs/sparkclaw.default.json"]

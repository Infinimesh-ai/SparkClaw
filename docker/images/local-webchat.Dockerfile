FROM node:26-alpine AS webchat-build
WORKDIR /src
COPY package.json package-lock.json ./
COPY apps/webchat/package.json apps/webchat/package.json
COPY tools/document-runtime/package.json tools/document-runtime/package.json
RUN npm ci
COPY apps/desktop/src/assets/icon.png apps/desktop/src/assets/icon.png
COPY apps/webchat apps/webchat
RUN npm --workspace @sparkclaw/webchat run build

FROM golang:1.25-alpine AS ingress-build
WORKDIR /src
COPY go.work ./
COPY services/gateway services/gateway
RUN CGO_ENABLED=0 go build -trimpath -o /out/local-webchat ./services/gateway/cmd/local-webchat

FROM alpine:3.22
RUN addgroup -g 10001 sparkclaw && adduser -D -u 10001 -G sparkclaw sparkclaw
COPY --from=ingress-build /out/local-webchat /usr/local/bin/local-webchat
COPY --from=webchat-build /src/apps/webchat/dist /usr/share/sparkclaw/webchat
ENV SPARKCLAW_LOCAL_WORKBENCH_RUNTIME_DIR=/run/sparkclaw/runtime \
    SPARKCLAW_LOCAL_WEBCHAT_ASSETS=/usr/share/sparkclaw/webchat
USER sparkclaw
ENTRYPOINT ["local-webchat"]

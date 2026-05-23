FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.work ./
COPY services/gateway/go.mod services/gateway/go.mod
COPY services/gateway services/gateway
RUN go build -o /out/sparkclaw ./services/gateway/cmd/sparkclaw

FROM alpine:3.22
WORKDIR /app
RUN adduser -D -u 10001 sparkclaw
COPY --from=build /out/sparkclaw /usr/local/bin/sparkclaw
COPY configs /app/configs
USER sparkclaw
EXPOSE 18789
ENTRYPOINT ["sparkclaw"]
CMD ["-config", "/app/configs/sparkclaw.default.json"]

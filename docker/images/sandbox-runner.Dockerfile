FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.work ./
COPY services/gateway/go.mod services/gateway/go.mod
COPY services/gateway services/gateway
RUN go build -o /out/sparkclaw-sandbox-runner ./services/gateway/cmd/sandbox-runner

FROM alpine:3.22
WORKDIR /app
RUN apk add --no-cache docker-cli
COPY --from=build /out/sparkclaw-sandbox-runner /usr/local/bin/sparkclaw-sandbox-runner
EXPOSE 18889
ENTRYPOINT ["sparkclaw-sandbox-runner"]

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.work ./
COPY services/gateway/go.mod services/gateway/go.mod
COPY services/gateway services/gateway
RUN go build -o /out/sparkclaw-evaluator ./services/gateway/cmd/evaluator

FROM alpine:3.24
WORKDIR /app
COPY --from=build /out/sparkclaw-evaluator /usr/local/bin/sparkclaw-evaluator
ENTRYPOINT ["sparkclaw-evaluator"]

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.work ./
COPY services/gateway/go.mod services/gateway/go.mod
COPY services/gateway services/gateway
RUN go build -o /out/sparkclaw-evaluator ./services/gateway/cmd/evaluator

FROM alpine:3.22
WORKDIR /app
COPY --from=build /out/sparkclaw-evaluator /usr/local/bin/sparkclaw-evaluator
ENTRYPOINT ["sparkclaw-evaluator"]

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG APM_TAG=""
# BUILD_TAGS controls compiled features:
#   sentry      - error tracking (included by default)
#   rabbitmq    - RabbitMQ queue driver
#   kafka       - Kafka queue driver
# Examples:
#   docker build --build-arg BUILD_TAGS="sentry rabbitmq" .
#   docker build --build-arg BUILD_TAGS="sentry kafka" .
#   docker build --build-arg BUILD_TAGS="sentry rabbitmq kafka" .
ARG BUILD_TAGS="sentry newrelic rabbitmq kafka"
RUN CGO_ENABLED=0 go build -tags "${BUILD_TAGS} ${APM_TAG}" -ldflags="-s -w -X main.version=${VERSION}" -o /express-botx .

FROM alpine:3.21 AS alpine
RUN apk add --no-cache ca-certificates
COPY --from=build /express-botx /usr/local/bin/express-botx
ENTRYPOINT ["express-botx"]

FROM scratch AS rootless
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /express-botx /express-botx
USER 65534:65534
ENTRYPOINT ["/express-botx"]

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE_NAME ?= express-botx
BUILD_TAGS = sentry newrelic kafka rabbitmq

.PHONY: build test lint fmt race docker-build version run

build:
	mkdir -p dist/
	go build -tags "$(BUILD_TAGS)" -ldflags="-s -w -X main.version=$(VERSION)" -o dist/express-botx .

test:
	go test -race -coverprofile=coverage.out -tags "$(BUILD_TAGS)" ./...
	@go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run

GO_FILES = $(shell find . -name '*.go' -not -path './vendor/*' -not -path './.ralphex/*')

fmt:
	goimports -w $(GO_FILES)

race:
	go test -race -timeout=60s -tags "$(BUILD_TAGS)" ./...

docker-build:
	docker build -t "$(IMAGE_NAME):$(VERSION)" .

version:
	@echo "$(VERSION)"

run:
	go run -tags "$(BUILD_TAGS)" . serve

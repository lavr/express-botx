VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE_NAME ?= express-botx
BUILD_TAGS = sentry newrelic kafka rabbitmq
TEST_TAGS  = sentry newrelic

# Escape single quotes for safe shell embedding in single-quoted strings
sq = $(subst ','\'',$(1))

.PHONY: build test lint fmt race docker-build version run

build:
	mkdir -p dist/
	go build -tags '$(call sq,$(BUILD_TAGS))' -ldflags='-s -w -X main.version=$(call sq,$(VERSION))' -o dist/express-botx .

test:
	go test -coverprofile=coverage.out -tags '$(call sq,$(TEST_TAGS))' ./...
	@go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run

fmt:
	find . -name '*.go' -not -path './vendor/*' -not -path './.ralphex/*' -exec goimports -w {} +

race:
	go test -race -timeout=60s -tags '$(call sq,$(TEST_TAGS))' ./...

docker-build:
	docker build --target alpine --build-arg VERSION='$(call sq,$(VERSION))' --build-arg BUILD_TAGS='$(call sq,$(BUILD_TAGS))' -t '$(call sq,$(IMAGE_NAME)):$(call sq,$(VERSION))' .

version:
	@echo '$(call sq,$(VERSION))'

run:
	go run -tags '$(call sq,$(BUILD_TAGS))' . serve

SHELL := /bin/sh

BUF_VERSION ?= 1.47.2
BUF ?= $(CURDIR)/bin/buf
PROTO_BREAKING_AGAINST ?= .git#branch=origin/main

.PHONY: proto-lint proto-generate proto-breaking buf-install generate check

buf-install:
	@mkdir -p $(CURDIR)/bin
	@if [ ! -x "$(BUF)" ]; then \
		GOBIN=$(CURDIR)/bin go install github.com/bufbuild/buf/cmd/buf@v$(BUF_VERSION); \
	fi

proto-lint: buf-install
	@$(BUF) lint

proto-generate: buf-install
	@$(BUF) generate

proto-breaking: buf-install
	@$(BUF) breaking --against '$(PROTO_BREAKING_AGAINST)'

generate: proto-generate

check: proto-lint
	@go test ./...
	@for module in example contrib/registry/etcd contrib/registry/consul contrib/telemetry/gorm contrib/telemetry/redis contrib/telemetry/otlphttp contrib/migrate/postgres contrib/stream/redis contrib/mid/ratelimit/redis contrib/mid/jwt; do \
		(cd $$module && go test ./...); \
	done
	@go test -race ./...
	@go test -count=1 -shuffle=on ./...
	@go vet ./...

SHELL := /bin/sh

BUF_VERSION ?= 1.47.2
BUF ?= $(CURDIR)/bin/buf

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
	@$(BUF) breaking --against '.git#branch=main'

generate: proto-generate

check: proto-lint
	@go test ./...
	@go test -race ./...
	@go test -count=1 -shuffle=on ./...
	@go vet ./...

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := darwin/arm64 linux/arm64 linux/amd64

export CGO_ENABLED := 0

.PHONY: build test vet dist clean

build:
	go build -ldflags '$(LDFLAGS)' -o backbeat ./cmd/backbeat

test:
	go test ./...

vet:
	go vet ./...

dist:
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		echo "==> $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -ldflags '$(LDFLAGS)' -o dist/backbeat-$$os-$$arch ./cmd/backbeat || exit 1; \
	done

clean:
	rm -rf backbeat dist

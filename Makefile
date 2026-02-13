BUILD_DIR = bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS = -ldflags "-X main.version=$(VERSION)"

.PHONY: all build build-tap build-tapd install uninstall clean test lint

all: build

build: build-tap build-tapd

build-tap:
	@echo "Building sql-tap..."
	go build $(LDFLAGS) -o $(BUILD_DIR)/sql-tap .

build-tapd:
	@echo "Building sql-tapd..."
	go build $(LDFLAGS) -o $(BUILD_DIR)/sql-tapd ./cmd/sql-tapd

install:
	@echo "Installing sql-tap and sql-tapd..."
	@bin_dir=$$(go env GOBIN); \
	if [ -z "$$bin_dir" ]; then \
		bin_dir=$$(go env GOPATH)/bin; \
	fi; \
	mkdir -p "$$bin_dir"; \
	echo "Installing to $$bin_dir"; \
	go build $(LDFLAGS) -o "$$bin_dir/sql-tap" .; \
	go build $(LDFLAGS) -o "$$bin_dir/sql-tapd" ./cmd/sql-tapd

uninstall:
	@echo "Uninstalling sql-tap and sql-tapd..."
	@bin_dir=$$(go env GOBIN); \
	if [ -z "$$bin_dir" ]; then \
		bin_dir=$$(go env GOPATH)/bin; \
	fi; \
	rm -f "$$bin_dir/sql-tap" "$$bin_dir/sql-tapd"

clean:
	@echo "Cleaning up..."
	rm -rf $(BUILD_DIR)

test:
	go test -race ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint is not installed"; \
		exit 1; \
	}
	golangci-lint run

.PHONY: build test run clean vet mcp all similar snapshot install

# Binary name
BIN := bin/guanfu
MCP_BIN := bin/guanfu-mcp
SIMILAR_BIN := bin/guanfu-similar

# Build flags
LDFLAGS := -s -w

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/guanfu

test:
	go test ./... -count=1

vet:
	go vet ./...

run: build
	GUANFU_NO_HISTORY=1 $(BIN)

run-json: build
	GUANFU_NO_HISTORY=1 $(BIN) --json

clean:
	rm -f $(BIN) $(MCP_BIN) $(SIMILAR_BIN)

# Install to $GOPATH/bin
install:
	go install ./cmd/guanfu

# MCP server
mcp:
	go build -o $(MCP_BIN) ./cmd/guanfu-mcp
	cp internal/client/futu_bridge.py bin/

similar:
	go build -o $(SIMILAR_BIN) ./cmd/guanfu-similar

all: vet test build mcp similar

snapshot:
	goreleaser release --snapshot --clean

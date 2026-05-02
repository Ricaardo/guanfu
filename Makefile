.PHONY: build test run clean

# Binary name
BIN := bin/guanfu

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
	rm -f $(BIN)

# Install to $GOPATH/bin
install:
	go install ./cmd/guanfu

all: vet test build

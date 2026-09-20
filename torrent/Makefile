.PHONY: build test vet lint clean gotorrent

# Build all packages
build:
	go build ./...

# Build CLI binary to ./bin/
gotorrent:
	go build -o bin/gotorrent ./cmd/gotorrent

# Run tests
test:
	go test ./... -race

# Run tests without the race detector (faster)
test-short:
	go test ./... -short

# Verbose test output
test-v:
	go test ./... -v -race

# Vet
vet:
	go vet ./...

# Vet + test
check: vet test

# Lint
lint:
	golangci-lint run ./...

# Clean build artifacts
clean:
	rm -rf bin/ && go clean ./...

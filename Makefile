APP       := viberouter
LDFLAGS   := -s -w
export CGO_ENABLED = 0

.PHONY: build build-windows build-linux build-linux-arm64 build-darwin build-all \
        run test test-sdk vet tidy clean help

# Build for the current platform (auto .exe on Windows)
build:
	go build -ldflags "$(LDFLAGS)" -o $(APP)$(shell go env GOEXE) .

build-windows:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(APP)-windows-amd64.exe .

build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(APP)-linux-amd64 .

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(APP)-linux-arm64 .

build-darwin:
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(APP)-darwin-arm64 .

build-all: build-windows build-linux build-linux-arm64 build-darwin

run:
	go run .

test:
	go test ./internal/...

test-sdk:
	go test ./test/sdk/

vet:
	go vet ./internal/... ./test/sdk/

tidy:
	go mod tidy

clean:
	rm -f $(APP) $(APP).exe \
	      $(APP)-windows-amd64.exe \
	      $(APP)-linux-amd64 $(APP)-linux-arm64 \
	      $(APP)-darwin-arm64

help:
	@echo "VibeRouter targets:"
	@echo "  make build              - current platform"
	@echo "  make build-windows      - windows/amd64 (.exe)"
	@echo "  make build-linux        - linux/amd64"
	@echo "  make build-linux-arm64  - linux/arm64"
	@echo "  make build-darwin       - macOS/arm64"
	@echo "  make build-all          - all of the above"
	@echo "  make run                - go run ."
	@echo "  make test               - routing unit tests"
	@echo "  make test-sdk           - SDK integration tests (needs running server)"
	@echo "  make vet / tidy / clean"

BINARY    := rpc
GO        := go
GOFLAGS   := -trimpath
GIT_HASH  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS   := -ldflags "-X main.buildVersion=$(GIT_HASH)"

.PHONY: build build-all build-linux push clean proto

build:
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY) .

# Build all platform/arch variants into bin/
build-all:
	GOOS=darwin  GOARCH=arm64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o bin/$(BINARY)_darwin_arm64 .
	GOOS=darwin  GOARCH=amd64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o bin/$(BINARY)_darwin_amd64 .
	GOOS=linux   GOARCH=arm64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o bin/$(BINARY)_linux_arm64  .
	GOOS=linux   GOARCH=amd64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o bin/$(BINARY)_linux_amd64  .

build-linux:
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY)_linux .

push: build-linux
	rsync -avz $(BINARY)_linux jahmelon@192.168.86.144:~/$(BINARY)

proto:
	protoc --go_out=. --go_opt=module=somewear/rpc proto/rpc.proto

clean:
	rm -f $(BINARY) $(BINARY)_linux bin/$(BINARY)_*

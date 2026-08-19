BINARY   := rpc
GO       := go
GOFLAGS  := -trimpath

.PHONY: build build-linux push clean proto

build:
	$(GO) build $(GOFLAGS) -o $(BINARY) .

build-linux:
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(BINARY)_linux .

push: build-linux
	rsync -avz $(BINARY)_linux jahmelon@192.168.86.144:~/$(BINARY)

proto:
	protoc --go_out=. --go_opt=module=somewear/rpc proto/rpc.proto

clean:
	rm -f $(BINARY) $(BINARY)_linux

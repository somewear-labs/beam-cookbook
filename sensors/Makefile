CMDS := ugs tws cbrn vimu seismograph sysmon

all: $(addprefix bin/, $(CMDS))

bin/%: cmd/%/main.go shared/send.go
	go build -o $@ ./cmd/$*/

linux-arm64: $(addprefix bin/linux-arm64/, $(CMDS))

bin/linux-arm64/%: cmd/%/main.go shared/send.go
	GOOS=linux GOARCH=arm64 go build -o $@ ./cmd/$*/

linux-amd64: $(addprefix bin/linux-amd64/, $(CMDS))

bin/linux-amd64/%: cmd/%/main.go shared/send.go
	GOOS=linux GOARCH=amd64 go build -o $@ ./cmd/$*/

clean:
	rm -rf bin/

.PHONY: all linux-arm64 linux-amd64 clean

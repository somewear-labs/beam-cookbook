CMDS := seismograph weather airquality accelerometer

all: $(addprefix bin/, $(CMDS))

bin/%: cmd/%/main.go shared/send.go
	go build -o $@ ./cmd/$*/

clean:
	rm -rf bin/

.PHONY: all clean

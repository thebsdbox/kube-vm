GO ?= go
BIN ?= kube-vm
OUTDIR ?= .
LDFLAGS ?=

.PHONY: all build clean

all: build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(OUTDIR)/$(BIN) .

clean:
	rm -f $(OUTDIR)/$(BIN)

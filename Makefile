GO ?= go
BIN ?= kube-vm
CLIENT ?= kubevmctl
OUTDIR ?= .
LDFLAGS ?=

.PHONY: all build build-vm build-client clean

all: build

build: build-vm build-client

build-vm:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(OUTDIR)/$(BIN) .

build-client:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(OUTDIR)/$(CLIENT) ./cmd/kubevmctl

clean:
	rm -f $(OUTDIR)/$(BIN) $(OUTDIR)/$(CLIENT)

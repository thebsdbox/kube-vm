GO ?= go
BIN ?= kube-vm
CLIENT ?= kube-vmctl
OUTDIR ?= bin
LDFLAGS ?=
DOCKER_TAG ?=
DOCKERFILE ?= 
DISK_DIR ?= disks
DISK_IMG ?= kube-vm-disk.img
MOUNT_DIR ?= /mnt

.PHONY: all build build-vm build-client clean demo exit-demo build-disk

all: build

build: build-vm build-client

build-vm:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(OUTDIR)/$(BIN) ./cmd/kube-vm
	sudo chown root:root $(OUTDIR)/$(BIN)
	sudo chmod u+s $(OUTDIR)/$(BIN)

build-client:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(OUTDIR)/$(CLIENT) ./cmd/kubevmctl
	sudo chown root:root $(OUTDIR)/$(CLIENT)
	sudo chmod u+s $(OUTDIR)/$(CLIENT)

clean:
	rm -f $(OUTDIR)/$(BIN) $(OUTDIR)/$(CLIENT)

demo: build
	screen -c ./kube-vm-screen.rc

exit-demo:
	screen -XS kube-vm-test quit

build-disk:
	@if [ -z "$(DOCKER_TAG)" ]; then \
		echo "DOCKER_TAG is required (example: make build-disk DOCKER_TAG=my-image)"; \
		exit 1; \
	fi
	@if [ -n "$(DOCKERFILE)" ]; then \
		docker build -t "$(DOCKER_TAG)" -f "$(DOCKERFILE)" .; \
	fi
	@set -eu; \
	uuid="$$(cat /proc/sys/kernel/random/uuid | cut -c1-8)"; \
	docker create --name="$$uuid" "$(DOCKER_TAG)" >/dev/null; \
	sudo dd if=/dev/zero of="$(DISK_DIR)/$(DISK_IMG)" bs=1 count=0 seek=2500M; \
	sudo mkfs.ext4 -F -E lazy_itable_init=0,lazy_journal_init=0 "$(DISK_DIR)/$(DISK_IMG)"; \
	sudo mount -o loop "$(DISK_DIR)/$(DISK_IMG)" "$(MOUNT_DIR)"; \
	docker export "$$uuid" | sudo tar x -C "$(MOUNT_DIR)"; \
	sync; \
	sudo umount "$(MOUNT_DIR)"; \
	sudo e2fsck -fy "$(DISK_DIR)/$(DISK_IMG)"; \
	docker rm "$$uuid" >/dev/null
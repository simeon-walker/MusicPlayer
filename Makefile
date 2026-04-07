# Makefile for mpd-controller

BINARY_NAME = mpd-controller
PI_HOST ?= $(MAKE_TARGET_HOST)
PI_PATH ?= $(MAKE_TARGET_PATH)

# Strip tools (adjust if your distro uses different names)
STRIP_ARM64 = aarch64-linux-gnu-strip
STRIP_ARMV7 = arm-linux-gnueabihf-strip

.PHONY: all clean build host arm64 armv7 sync

all: clean build

# Build for host (whatever OS/arch you run make on)
host:
	@echo "Building for host..."
	go build -o $(BINARY_NAME)

# Build for Raspberry Pi 4 (64-bit OS)
arm64:
	@echo "Building for Raspberry Pi (arm64)..."
	CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc GOOS=linux GOARCH=arm64 go build -o $(BINARY_NAME)-arm64
	@echo "Stripping binary with $(STRIP_ARM64)..."
	$(STRIP_ARM64) $(BINARY_NAME)-arm64 || echo "Warning: $(STRIP_ARM64) not found, skipping strip"

# Build for Raspberry Pi (32-bit OS)
armv7:
	@echo "Building for Raspberry Pi (armv7)..."
	CGO_ENABLED=1 CC=arm-linux-gnueabihf-gcc GOOS=linux GOARCH=arm GOARM=7 go build -o $(BINARY_NAME)-armv7
	@echo "Stripping binary with $(STRIP_ARMV7)..."
	$(STRIP_ARMV7) $(BINARY_NAME)-armv7 || echo "Warning: $(STRIP_ARMV7) not found, skipping strip"

# Default build for local development.
# Deployment flow is: make sync, then build on the Pi.
build: host

# Clean build outputs
clean:
	@echo "Cleaning..."
	rm -f $(BINARY_NAME) $(BINARY_NAME)-arm64 $(BINARY_NAME)-armv7

sync:
	@set -eu; \
	host="$(PI_HOST)"; \
	path="$(PI_PATH)"; \
	if [ -z "$$host" ]; then \
		echo "Error: PI_HOST is empty. Set PI_HOST or MAKE_TARGET_HOST."; \
		exit 1; \
	fi; \
	if [ -z "$$path" ]; then \
		echo "Error: PI_PATH is empty. Set PI_PATH or MAKE_TARGET_PATH."; \
		exit 1; \
	fi; \
	echo "syncing to $$host:$$path"; \
	rsync -av --delete \
		--exclude=mpd-controller \
		--exclude=musicplayer \
		--exclude=.git \
		--exclude=.vscode \
		--exclude=files \
		--exclude=.env . \
		"$$host:$$path"
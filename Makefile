# Makefile for mpd-controller

BINARY_NAME = mpd-controller
PI_HOST = pi@raspberrypi   # <-- change to your Pi's username@hostname or IP
PI_PATH = /home/pi         # <-- change to where you want the binary installed

# Strip tools (adjust if your distro uses different names)
STRIP_ARM64 = aarch64-linux-gnu-strip
STRIP_ARMV7 = arm-linux-gnueabihf-strip

.PHONY: all clean build host arm64 armv7 deploy

all: clean build

# Build for host (whatever OS/arch you run make on)
host:
	@echo "Building for host..."
	go build -o $(BINARY_NAME)

# Build for Raspberry Pi 4 (64-bit OS)
arm64:
	@echo "Building for Raspberry Pi (arm64)..."
	GOOS=linux GOARCH=arm64 go build -o $(BINARY_NAME)-arm64
	@echo "Stripping binary with $(STRIP_ARM64)..."
	$(STRIP_ARM64) $(BINARY_NAME)-arm64 || echo "Warning: $(STRIP_ARM64) not found, skipping strip"

# Build for Raspberry Pi (32-bit OS)
armv7:
	@echo "Building for Raspberry Pi (armv7)..."
	GOOS=linux GOARCH=arm GOARM=7 go build -o $(BINARY_NAME)-armv7
	@echo "Stripping binary with $(STRIP_ARMV7)..."
	$(STRIP_ARMV7) $(BINARY_NAME)-armv7 || echo "Warning: $(STRIP_ARMV7) not found, skipping strip"

# Build all
build: host arm64 armv7

# Deploy to Raspberry Pi (default: arm64 binary)
deploy: arm64
	@echo "Deploying $(BINARY_NAME)-arm64 to $(PI_HOST):$(PI_PATH)..."
	scp $(BINARY_NAME)-arm64 $(PI_HOST):$(PI_PATH)/$(BINARY_NAME)
	@echo "Deployed successfully!"

# Clean build outputs
clean:
	@echo "Cleaning..."
	rm -f $(BINARY_NAME) $(BINARY_NAME)-arm64 $(BINARY_NAME)-armv7

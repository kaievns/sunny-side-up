# paneld - build for the local dev machine (macOS/Linux) and cross-compile for
# the NanoPi R5C router (OpenWrt, aarch64/musl).
#
# The binary uses cgo to link libftdi1, so a native build needs libftdi + a C
# compiler, and a cross build needs a musl cross-toolchain plus the target's
# libftdi1/libusb-1.0. See the two cross recipes below.

BINDIR ?= bin
PKG    := ./cmd/bringup

# Homebrew's pkg-config/libftdi may not be on a minimal PATH; add common prefixes.
export PATH := /opt/homebrew/bin:/usr/local/bin:$(PATH)

.PHONY: all dev run tidy vet clean router-zig router-sdk

all: dev

## dev: native build for this machine (needs: brew install libftdi pkg-config)
dev: tidy
	CGO_ENABLED=1 go build -o $(BINDIR)/bringup $(PKG)
	@echo "built $(BINDIR)/bringup"

## run: build and run the bring-up test against a plugged-in board
run:
	CGO_ENABLED=1 go run $(PKG) $(ARGS)

tidy:
	go mod tidy

vet:
	CGO_ENABLED=1 go vet ./...

clean:
	rm -rf $(BINDIR)

# ---------------------------------------------------------------------------
# Cross-compile for the router (OpenWrt aarch64, musl libc).
#
# Path A - zig as the cgo C compiler (no OpenWrt SDK needed on your Mac).
#   Needs: `brew install zig`, and a SYSROOT that contains the target's
#   libftdi1 + libusb-1.0 headers/libs (copy /usr/include & /usr/lib from the
#   router, or unpack the .ipk files). Then:
#
#     make router-zig SYSROOT=/path/to/aarch64-sysroot
#
SYSROOT ?= /path/to/aarch64-sysroot
router-zig:
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
	CC="zig cc -target aarch64-linux-musl --sysroot=$(SYSROOT)" \
	CGO_CFLAGS="-I$(SYSROOT)/usr/include -I$(SYSROOT)/usr/include/libftdi1" \
	CGO_LDFLAGS="-L$(SYSROOT)/usr/lib -lftdi1 -lusb-1.0" \
	go build -o $(BINDIR)/bringup-arm64 $(PKG)
	@echo "built $(BINDIR)/bringup-arm64 - scp it to the router"

# Path B - OpenWrt SDK toolchain (most reliable for OpenWrt).
#   Download the SDK for your target (rockchip/armv8), then inside it:
#     ./scripts/feeds update -a && ./scripts/feeds install libftdi1
#     make package/libftdi1/compile
#   which populates staging_dir with libftdi1/libusb. Point STAGING_DIR and
#   TOOLCHAIN at the SDK and run:
#
#     make router-sdk STAGING_DIR=... TOOLCHAIN=...
#
STAGING_DIR ?= /path/to/openwrt-sdk/staging_dir/target-aarch64_generic_musl
TOOLCHAIN   ?= /path/to/openwrt-sdk/staging_dir/toolchain-aarch64_generic_gcc-.._musl
router-sdk:
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
	CC="$(TOOLCHAIN)/bin/aarch64-openwrt-linux-musl-gcc" \
	CGO_CFLAGS="-I$(STAGING_DIR)/usr/include -I$(STAGING_DIR)/usr/include/libftdi1" \
	CGO_LDFLAGS="-L$(STAGING_DIR)/usr/lib -lftdi1 -lusb-1.0" \
	go build -o $(BINDIR)/bringup-arm64 $(PKG)
	@echo "built $(BINDIR)/bringup-arm64 - scp it to the router"

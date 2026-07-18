# paneld - build for the local dev machine (macOS/Linux) and cross-compile for
# the NanoPi R5C router (OpenWrt, aarch64/musl).
#
# The binary links libftdi1 via cgo. Native builds link the system libftdi;
# the router build uses zig as a musl cross-compiler and links against a
# generated stub libftdi1.so.2 (the real one comes from `apk add libftdi1`
# on the router at runtime).

BINDIR ?= bin

# Homebrew tools (zig, go) may not be on a minimal PATH.
export PATH := /opt/homebrew/bin:/usr/local/bin:$(PATH)

# native bench/dev commands (cgo, need: brew install libftdi)
CMDS := paneld bringup screens tinycheck white

.PHONY: all dev run tidy vet clean router cross-libs

all: dev

## dev: native build of every command into bin/
dev: tidy
	@for c in $(CMDS); do \
		CGO_ENABLED=1 go build -o $(BINDIR)/$$c ./cmd/$$c || exit 1; \
		echo "built $(BINDIR)/$$c"; \
	done

## run: build and run the bring-up smoke test against a plugged-in board
run:
	CGO_ENABLED=1 go run ./cmd/bringup $(ARGS)

tidy:
	go mod tidy

vet:
	CGO_ENABLED=1 go vet ./...

clean:
	rm -rf $(BINDIR) sysroot

# ---------------------------------------------------------------------------
# Cross-compile for the router (OpenWrt aarch64 / musl).
#
#   make router          # -> bin/paneld-arm64
#
# Requires zig (brew install zig). scp -O the result to the router; on the
# router run `apk add libftdi1` once (pulls libusb-1.0) for the runtime lib.
ZIG          ?= zig
CROSS_TARGET ?= aarch64-linux-musl
SYSROOT      := $(CURDIR)/sysroot/aarch64

router: cross-libs
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
	CC="$(ZIG) cc -target $(CROSS_TARGET)" \
	CXX="$(ZIG) c++ -target $(CROSS_TARGET)" \
	CGO_LDFLAGS="-L$(SYSROOT)" \
	go build -trimpath -ldflags "-s -w" -o $(BINDIR)/paneld-arm64 ./cmd/paneld
	@command -v file >/dev/null 2>&1 && file $(BINDIR)/paneld-arm64 || true
	@echo "built $(BINDIR)/paneld-arm64 - scp -O to the router (needs: apk add libftdi1)"

## cross-libs: generate the aarch64 stub libftdi1.so.2 used only for linking
cross-libs:
	@ZIG="$(ZIG)" TARGET="$(CROSS_TARGET)" ./scripts/make-cross-libs.sh

# Alternative: build against the router's REAL libftdi1 for extra certainty about
# the soname/symbols. Copy them off the router first, e.g.:
#   scp -O root@ROUTER:/usr/lib/libftdi1.so.2 root@ROUTER:/usr/lib/libusb-1.0.so.0 sysroot/aarch64/
#   ( cd sysroot/aarch64 && ln -sf libftdi1.so.2 libftdi1.so )
# then run `make router` (it will link against whatever is in sysroot/aarch64).

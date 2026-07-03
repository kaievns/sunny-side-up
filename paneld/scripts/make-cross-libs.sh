#!/bin/sh
# Generate a link-time stub libftdi1.so.2 for aarch64-linux-musl.
#
# The paneld binary links libftdi1 via cgo. To cross-compile it for the router
# we only need SOMETHING named libftdi1 with the right symbols and soname for the
# linker to resolve against - the REAL libftdi1.so.2 is provided at runtime on
# the router by `opkg install libftdi1`. So we build a tiny stub whose function
# bodies are never executed. This keeps cross-compilation independent of the
# OpenWrt version and package format (.ipk vs .apk) and needs no target headers.
set -eu

ZIG="${ZIG:-zig}"
TARGET="${TARGET:-aarch64-linux-musl}"
HERE=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
OUT="${OUT:-$HERE/sysroot/aarch64}"

if ! command -v "$ZIG" >/dev/null 2>&1; then
  echo "error: '$ZIG' not found. Install it: brew install zig" >&2
  exit 1
fi

mkdir -p "$OUT"
STUB=$(mktemp -t ftdi-stub-XXXXXX).c
trap 'rm -f "$STUB"' EXIT

cat > "$STUB" <<'EOF'
/* Link-time stub for libftdi1 - bodies are never called. At runtime the real
   libftdi1.so.2 (opkg install libftdi1) is loaded and provides these symbols.
   The signatures only need to match names; types are ABI-compatible stand-ins. */
void *ftdi_new(void) { return 0; }
void  ftdi_free(void *a) { (void)a; }
int   ftdi_set_interface(void *a, int b) { (void)a; (void)b; return 0; }
int   ftdi_usb_open(void *a, int b, int c) { (void)a; (void)b; (void)c; return 0; }
int   ftdi_usb_close(void *a) { (void)a; return 0; }
int   ftdi_usb_reset(void *a) { (void)a; return 0; }
int   ftdi_set_latency_timer(void *a, unsigned char b) { (void)a; (void)b; return 0; }
int   ftdi_set_bitmode(void *a, unsigned char b, unsigned char c) { (void)a; (void)b; (void)c; return 0; }
int   ftdi_write_data(void *a, const unsigned char *b, int c) { (void)a; (void)b; (void)c; return 0; }
int   ftdi_read_data(void *a, unsigned char *b, int c) { (void)a; (void)b; (void)c; return 0; }
int   ftdi_tcioflush(void *a) { (void)a; return 0; }
char *ftdi_get_error_string(void *a) { (void)a; return 0; }
EOF

echo "generating stub libftdi1.so.2 for $TARGET -> $OUT"
$ZIG cc -target "$TARGET" -shared -fPIC -nostdlib \
  -Wl,-soname,libftdi1.so.2 \
  -o "$OUT/libftdi1.so.2" "$STUB"
ln -sf libftdi1.so.2 "$OUT/libftdi1.so"

echo "done:"
ls -l "$OUT"

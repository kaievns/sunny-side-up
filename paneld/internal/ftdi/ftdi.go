// Package ftdi is a thin driver for the FT232H on the lcd-panel board, used in
// MPSSE mode to speak SPI to the LCD and to bit-bang GPIO for the LCD control
// lines, the fan MOSFET gate, and the fan tachometer.
//
// This file (ftdi.go) is the only cgo in the project: it wraps just the handful
// of libftdi1 calls we need as a raw byte transport. All of the MPSSE protocol
// logic lives in mpsse.go as plain Go on top of Write/Read.
//
// Build requirements:
//
//	macOS dev:   brew install libftdi     (links /opt/homebrew/lib/libftdi1.dylib)
//	OpenWrt:     opkg install libftdi1     (provides libftdi1.so.2 + libusb at runtime)
//	cross-build: make router               (zig cc + a generated stub libftdi1.so.2)
//
// The cgo binding is deliberately header-free: we declare the few libftdi1
// functions we use, so cross-compiling needs no target headers - only something
// named libftdi1 to link against. See the Makefile 'router' target.
package ftdi

/*
#cgo darwin LDFLAGS: -L/opt/homebrew/lib -L/usr/local/lib -lftdi1
#cgo linux  LDFLAGS: -lftdi1

// Minimal libftdi1 API surface (cf. upstream <ftdi.h>). struct ftdi_context is
// opaque here - we only ever hold a pointer to it, never touch its fields.
struct ftdi_context;
struct ftdi_context *ftdi_new(void);
void  ftdi_free(struct ftdi_context *ftdi);
int   ftdi_set_interface(struct ftdi_context *ftdi, int iface);
int   ftdi_usb_open(struct ftdi_context *ftdi, int vendor, int product);
int   ftdi_usb_close(struct ftdi_context *ftdi);
int   ftdi_usb_reset(struct ftdi_context *ftdi);
int   ftdi_set_latency_timer(struct ftdi_context *ftdi, unsigned char latency);
int   ftdi_set_bitmode(struct ftdi_context *ftdi, unsigned char bitmask, unsigned char mode);
int   ftdi_write_data(struct ftdi_context *ftdi, const unsigned char *buf, int size);
int   ftdi_read_data(struct ftdi_context *ftdi, unsigned char *buf, int size);
int   ftdi_tcioflush(struct ftdi_context *ftdi);
char *ftdi_get_error_string(struct ftdi_context *ftdi);
*/
import "C"

import (
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// Default USB identity of a stock FT232H (blank/unprogrammed EEPROM). The
// board's 93LC56 is unprogrammed, so the chip enumerates as FTDI's default.
const (
	DefaultVID = 0x0403
	DefaultPID = 0x6014
)

// libftdi interface/mode values (from <ftdi.h>): INTERFACE_A, BITMODE_RESET,
// BITMODE_MPSSE. Declared here since we don't include the header.
const (
	ifaceA       = 1
	bitmodeReset = 0x00
	bitmodeMPSSE = 0x02
)

// Device is an open FT232H in MPSSE mode. It is safe for concurrent use: the
// LCD and fan share a single physical chip and, worse, share the MPSSE low byte
// (the fan gate sits on the same 8-bit port as every SPI/LCD control line), so
// all access is serialised through mu and a shadow copy of the low byte.
type Device struct {
	mu  sync.Mutex
	ctx *C.struct_ftdi_context

	lowShadow byte // last value written to the ADBUS low byte
	lowDir    byte // ADBUS direction mask (1 = output)
}

// Open finds and opens the first FT232H with the given VID/PID and puts it into
// MPSSE mode. Pass 0/0 to use the FT232H defaults.
//
// On Linux the kernel ftdi_sio driver may have claimed the chip as a ttyUSB;
// libftdi auto-detaches it (module_detach_mode defaults to
// AUTO_DETACH_SIO_MODULE). On macOS the Apple FTDI driver is detached the same
// way via libusb.
func Open(vid, pid int) (*Device, error) {
	if vid == 0 {
		vid = DefaultVID
	}
	if pid == 0 {
		pid = DefaultPID
	}

	ctx := C.ftdi_new()
	if ctx == nil {
		return nil, fmt.Errorf("ftdi: ftdi_new failed (out of memory?)")
	}
	d := &Device{ctx: ctx}

	// From here on every error path captures the libftdi diagnostic BEFORE
	// Close frees the context (Close sets d.ctx = nil), so wrapErr never reads a
	// freed/nil context and we don't leak the ftdi_new allocation.
	if rc := C.ftdi_set_interface(ctx, C.int(ifaceA)); rc < 0 {
		err := d.wrapErr("ftdi_set_interface", rc)
		d.Close()
		return nil, err
	}

	if rc := C.ftdi_usb_open(ctx, C.int(vid), C.int(pid)); rc < 0 {
		err := d.wrapErr("ftdi_usb_open", rc)
		d.Close()
		return nil, fmt.Errorf("%w (is the board plugged in? on Linux try: modprobe -r ftdi_sio)", err)
	}

	if rc := C.ftdi_usb_reset(ctx); rc < 0 {
		err := d.wrapErr("ftdi_usb_reset", rc)
		d.Close()
		return nil, err
	}

	// A short USB latency timer is essential: without it the FTDI holds small
	// reads for up to 16ms, which would cripple tach sampling. 1ms is the min.
	if rc := C.ftdi_set_latency_timer(ctx, 1); rc < 0 {
		err := d.wrapErr("ftdi_set_latency_timer", rc)
		d.Close()
		return nil, err
	}

	// Reset the bit mode, flush, then enable MPSSE.
	if rc := C.ftdi_set_bitmode(ctx, C.uchar(0x00), C.uchar(bitmodeReset)); rc < 0 {
		err := d.wrapErr("ftdi_set_bitmode(reset)", rc)
		d.Close()
		return nil, err
	}
	if err := d.flush(); err != nil {
		d.Close()
		return nil, err
	}
	if rc := C.ftdi_set_bitmode(ctx, C.uchar(0x00), C.uchar(bitmodeMPSSE)); rc < 0 {
		err := d.wrapErr("ftdi_set_bitmode(mpsse)", rc)
		d.Close()
		return nil, err
	}
	// Let the MPSSE engine settle before issuing commands (FTDI AN-135).
	time.Sleep(50 * time.Millisecond)

	return d, nil
}

// Close releases the chip. It is safe to call more than once.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx == nil {
		return nil
	}
	C.ftdi_usb_close(d.ctx)
	C.ftdi_free(d.ctx)
	d.ctx = nil
	return nil
}

// flush drops any stale bytes in the FTDI's RX/TX buffers.
func (d *Device) flush() error {
	if rc := C.ftdi_tcioflush(d.ctx); rc < 0 {
		return d.wrapErr("ftdi_tcioflush", rc)
	}
	return nil
}

// write sends raw bytes to the FT232H. Caller must hold d.mu.
func (d *Device) write(buf []byte) error {
	if len(buf) == 0 {
		return nil
	}
	n := C.ftdi_write_data(d.ctx, (*C.uchar)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
	if n < 0 {
		return d.wrapErr("ftdi_write_data", n)
	}
	if int(n) != len(buf) {
		return fmt.Errorf("ftdi: short write: wrote %d of %d bytes", int(n), len(buf))
	}
	return nil
}

// read pulls up to len(buf) bytes. It returns the count read; the FTDI may
// return fewer (or zero) bytes than requested if they aren't available yet.
// Caller must hold d.mu.
func (d *Device) read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	n := C.ftdi_read_data(d.ctx, (*C.uchar)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
	if n < 0 {
		return 0, d.wrapErr("ftdi_read_data", n)
	}
	return int(n), nil
}

func (d *Device) wrapErr(op string, rc C.int) error {
	// Guard against a freed/nil context: some libftdi builds' get_error_string
	// dereferences without a NULL check.
	if d.ctx == nil {
		return fmt.Errorf("ftdi: %s failed (rc=%d)", op, int(rc))
	}
	msg := C.GoString(C.ftdi_get_error_string(d.ctx))
	return fmt.Errorf("ftdi: %s failed (rc=%d): %s", op, int(rc), msg)
}

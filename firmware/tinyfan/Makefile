# tinyfan — ATtiny414 firmware build + flash (SerialUPDI via the UPDI Friend)
#
#   make              build tinyfan.hex (the product firmware)
#   make flash        flash it over UPDI (PORT/BAUD overridable)
#   make sig          read the device signature (link test, no firmware)
#   make fuses        dump the fuse block
#
# Bring-up diagnostics (build DIAG=<name>, flash flash-<name>):
#   blink    raw-GPIO blink of BL + fan gate — "does the chip execute at all";
#            no peripherals, runs on the reset-default clock
#   bringup  TCA0 PWM on BL + fan — "do the PWM outputs and power stages work"
#   spiecho  SPI slave that echoes every received byte — "is the SPI wire
#            clean"; pair with `tinycheck -debug` (sent vs heard, offset by 1)
#
#   make blink.hex && make flash-blink     # etc.
#
# Toolchain: avr-gcc with tinyAVR-1 support. macOS:
#   brew tap osx-cross/avr && brew trust osx-cross/avr && brew install avr-gcc@14
#   PATH += /opt/homebrew/opt/avr-gcc@14/bin:/opt/homebrew/opt/avr-binutils/bin

MCU     = attiny414
F_CPU   = 10000000UL

CC      = avr-gcc
OBJCOPY = avr-objcopy
SIZE    = avr-size

CFLAGS  = -mmcu=$(MCU) -DF_CPU=$(F_CPU) -Os -std=gnu11 \
          -Wall -Wextra -flto -ffunction-sections -fdata-sections
LDFLAGS = -Wl,--gc-sections

# SerialUPDI programmer (Adafruit UPDI Friend or any USB-UART dongle).
# Board TC2030 pads (as fabbed): pad 1 = 3V3, pad 3 = UPDI, pad 5 = GND.
PORT   ?= /dev/cu.usbserial-133110
BAUD   ?= 115200
AVRDUDE = avrdude -c serialupdi -P $(PORT) -b $(BAUD) -p $(MCU)

TARGET = tinyfan

all: $(TARGET).hex

$(TARGET).elf: main.c crc8_tab.inc
	$(CC) $(CFLAGS) $(LDFLAGS) main.c -o $@
	$(SIZE) $@

# diagnostics: any of blink / bringup / spiecho
%.elf: %.c
	$(CC) $(CFLAGS) $(LDFLAGS) $< -o $@
	$(SIZE) $@

%.hex: %.elf
	$(OBJCOPY) -O ihex -R .eeprom $< $@

flash: $(TARGET).hex
	$(AVRDUDE) -U flash:w:$<:i

flash-%: %.hex
	$(AVRDUDE) -U flash:w:$<:i

sig:
	$(AVRDUDE) -U signature:r:-:h

fuses:
	$(AVRDUDE) -U fuse2:r:-:h -U fuse5:r:-:h

clean:
	rm -f *.elf *.hex

.PHONY: all flash sig fuses clean

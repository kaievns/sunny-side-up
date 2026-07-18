// Command white paints the whole panel a solid colour and exits. The NV3007
// retains its frame RAM, so the image persists after the tool quits — useful
// on the v2 bench where the ATtiny owns the backlight and the glass otherwise
// shows black pixels (which swallow the BL, making BL diagnostics invisible).
//
//	sudo /tmp/white              # full white
//	sudo /tmp/white -level 128   # grey
package main

import (
	"flag"
	"fmt"
	"image/color"
	"log"

	"github.com/kaievns/sunny-side-up/paneld/internal/lcd"
	"github.com/kaievns/sunny-side-up/paneld/internal/panel"
)

func main() {
	clockHz := flag.Int("clock", 15_000_000, "SPI clock in Hz")
	vid := flag.Int("vid", 0, "USB vendor id (0 = FT232H default)")
	pid := flag.Int("pid", 0, "USB product id (0 = FT232H default)")
	rotate := flag.Int("rotate", 270, "display rotation")
	level := flag.Int("level", 255, "grey level 0-255 (255 = white)")
	flag.Parse()

	cfg := panel.Config{ClockHz: *clockHz, VID: *vid, PID: *pid,
		LCD: lcd.Options{Rotation: *rotate}}
	p, err := panel.Open(cfg)
	if err != nil {
		log.Fatalf("white: %v", err)
	}
	defer p.Close()

	fb := lcd.NewFramebuffer(p.Display.Width(), p.Display.Height())
	v := uint8(*level)
	fb.Fill(color.RGBA{v, v, v, 255})
	if err := p.Display.Blit(fb); err != nil {
		log.Fatalf("white: %v", err)
	}
	fmt.Printf("painted %dx%d at level %d; frame RAM retains it after exit\n",
		p.Display.Width(), p.Display.Height(), *level)
}

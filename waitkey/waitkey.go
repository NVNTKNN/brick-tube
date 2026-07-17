// waitkey: block until the STOP button (B or MENU) is pressed, then exit 0.
// Ignores volume, D-pad, and other buttons so playback isn't killed by volume
// changes. Ignores presses in the first 600ms (the launch button, still buffered).
package main

import (
	"encoding/binary"
	"os"
	"time"
)

// stop buttons: 305 = B (BTN_EAST), 316 = MENU (BTN_MODE)
var stopCodes = map[uint16]bool{305: true, 316: true}

func main() {
	dev := "/dev/input/event3" // TRIMUI Player1 gamepad
	if len(os.Args) > 1 {
		dev = os.Args[1]
	}
	f, err := os.Open(dev)
	if err != nil {
		os.Exit(1)
	}
	defer f.Close()
	start := time.Now()
	buf := make([]byte, 24)
	for {
		n, err := f.Read(buf)
		if err != nil || n < 24 {
			continue
		}
		etype := binary.LittleEndian.Uint16(buf[16:18])
		code := binary.LittleEndian.Uint16(buf[18:20])
		value := int32(binary.LittleEndian.Uint32(buf[20:24]))
		if etype == 1 && value == 1 && stopCodes[code] && time.Since(start) > 600*time.Millisecond {
			os.Exit(0)
		}
	}
}

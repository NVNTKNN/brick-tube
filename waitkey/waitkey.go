// waitkey: block until a FRESH gamepad button press, then exit 0. Ignores key
// events in the first 600ms so the button that launched playback (still buffered
// in the evdev queue) doesn't instantly quit. Used by the YouTube pak to stop
// tplayerdemo (which has no button-quit of its own).
package main

import (
	"encoding/binary"
	"os"
	"time"
)

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
	buf := make([]byte, 24) // input_event on arm64: 16 (timeval) + 2 + 2 + 4
	for {
		n, err := f.Read(buf)
		if err != nil || n < 24 {
			continue
		}
		etype := binary.LittleEndian.Uint16(buf[16:18])
		value := int32(binary.LittleEndian.Uint32(buf[20:24]))
		if etype == 1 && value == 1 && time.Since(start) > 600*time.Millisecond {
			os.Exit(0)
		}
	}
}

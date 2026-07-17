// keyprobe: log each key event (code + press/release) from an input device, so
// we can map physical buttons to codes. Writes to stdout; run for a few seconds.
package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

func main() {
	dev := "/dev/input/event3"
	if len(os.Args) > 1 {
		dev = os.Args[1]
	}
	f, err := os.Open(dev)
	if err != nil {
		fmt.Println("open err:", err)
		os.Exit(1)
	}
	defer f.Close()
	buf := make([]byte, 24)
	for {
		n, err := f.Read(buf)
		if err != nil || n < 24 {
			continue
		}
		etype := binary.LittleEndian.Uint16(buf[16:18])
		code := binary.LittleEndian.Uint16(buf[18:20])
		value := int32(binary.LittleEndian.Uint32(buf[20:24]))
		if etype == 1 { // EV_KEY
			state := "release"
			if value == 1 {
				state = "PRESS"
			} else if value == 2 {
				state = "repeat"
			}
			fmt.Printf("code=%d %s\n", code, state)
		}
	}
}

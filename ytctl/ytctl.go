// ytctl: gamepad -> tplayerdemo stdin-FIFO controller for the YouTube pak.
// Replaces waitkey: instead of just exiting on a button, it drives the player
// over its stdin FIFO so the Allwinner video layer is torn down cleanly
// (TPlayerDestroy) — no more grey screen after stop.
//
// usage: ytctl <fifo> <tplayer-pid> <logfile> [eventdev]
//   MENU (316)        -> "quit" (clean layer teardown) -> exit 0
//   A/B (304/305)     -> "pause"/"play" toggle (probe was ambiguous about which
//                        code is the physical B, so both toggle; neither stops)
//   volume (114/115)  -> ignored — volume must never stop playback
// exit codes: 0 = user stopped, 2 = player died on its own, 3 = video finished
// (TPLAYER_NOTIFY_PLAYBACK_COMPLETE seen in the log; quit sent -> back to list).
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
)

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: ytctl <fifo> <tplayer-pid> <logfile> [eventdev]")
		os.Exit(1)
	}
	fifoPath := os.Args[1]
	pid, err := strconv.Atoi(os.Args[2])
	if err != nil || pid <= 0 {
		os.Exit(1)
	}
	logPath := os.Args[3]
	dev := "/dev/input/event3" // TRIMUI Player1 gamepad
	if len(os.Args) > 4 {
		dev = os.Args[4]
	}

	ev, err := os.Open(dev)
	if err != nil {
		os.Exit(1)
	}
	// The FIFO already has tplayerdemo reading it, so this open won't block.
	fifo, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
	if err != nil {
		os.Exit(1)
	}

	send := func(cmd string) bool {
		_, werr := fmt.Fprintf(fifo, "%s\n", cmd)
		return werr == nil
	}
	quitAndWait := func() {
		send("quit")
		for i := 0; i < 30 && alive(pid); i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}

	keys := make(chan uint16, 8)
	go func() {
		buf := make([]byte, 24) // struct input_event, 64-bit: 16B timeval + type/code/value
		for {
			n, rerr := ev.Read(buf)
			if rerr != nil {
				close(keys)
				return
			}
			if n < 24 {
				continue
			}
			etype := binary.LittleEndian.Uint16(buf[16:18])
			code := binary.LittleEndian.Uint16(buf[18:20])
			value := int32(binary.LittleEndian.Uint32(buf[20:24]))
			if etype == 1 && value == 1 { // key press only (no repeat/release)
				select {
				case keys <- code:
				default:
				}
			}
		}
	}()

	// Watch only log lines appended after we start, so a COMPLETE from an
	// earlier video can't end this one.
	var logOff int64
	if st, serr := os.Stat(logPath); serr == nil {
		logOff = st.Size()
	}
	completed := func() bool {
		f, oerr := os.Open(logPath)
		if oerr != nil {
			return false
		}
		defer f.Close()
		st, serr := f.Stat()
		if serr != nil || st.Size() <= logOff {
			return false
		}
		if _, serr := f.Seek(logOff, 0); serr != nil {
			return false
		}
		chunk := make([]byte, st.Size()-logOff)
		n, _ := f.Read(chunk)
		if bytes.Contains(chunk[:n], []byte("TPLAYER_NOTIFY_PLAYBACK_COMPLETE")) {
			return true
		}
		logOff += int64(n)
		if logOff > 64 {
			logOff -= 64 // overlap so a marker split across reads still matches
		}
		return false
	}

	start := time.Now()
	paused := false
	tick := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case code, ok := <-keys:
			if !ok {
				keys = nil // input device gone; keep watching the player
				continue
			}
			if time.Since(start) < 600*time.Millisecond {
				continue // launch press still buffered
			}
			switch code {
			case 316: // MENU: clean stop
				quitAndWait()
				os.Exit(0)
			case 304, 305: // A or B: pause/resume
				if paused {
					if !send("play") {
						os.Exit(2)
					}
					paused = false
				} else {
					if !send("pause") {
						os.Exit(2)
					}
					paused = true
				}
			}
		case <-tick.C:
			if !alive(pid) {
				os.Exit(2)
			}
			if completed() {
				quitAndWait()
				os.Exit(3)
			}
		}
	}
}

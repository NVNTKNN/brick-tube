// ytctl: gamepad -> tplayerdemo stdin-FIFO controller for the YouTube pak.
// Drives the player over its stdin FIFO so the Allwinner video layer tears down
// cleanly (TPlayerDestroy) — no grey screen after stop. Also owns a pause-only
// progress bar (drawn into the fb0 letterbox strip) and D-pad scrubbing.
//
// usage: ytctl <fifo> <tplayer-pid> <logfile> [eventdev] [duration-seconds]
//   MENU (316)          -> "quit" (clean layer teardown) -> exit 0
//   A/B (304/305)       -> pause/resume toggle (both, probe ambiguity harmless)
//   D-pad LEFT/RIGHT    -> seek -/+10s (hat-axis or key form); works while
//                          playing or paused
//   volume (114/115)    -> ignored
// On pause: a progress bar is drawn in the bottom letterbox strip of /dev/fb0;
// it clears on resume. Position is tracked from wall-clock + seeks (no player
// position readback needed).
// exit codes: 0 = user stopped, 2 = player died, 3 = video finished.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// ---- framebuffer progress bar --------------------------------------------

type fb struct {
	data   []byte
	w, h   int
	stride int
	bpp    int
}

func readIntFile(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func openFB() *fb {
	// geometry from sysfs avoids marshaling the fb_var/fix_screeninfo ioctls
	sz, err := os.ReadFile("/sys/class/graphics/fb0/virtual_size")
	if err != nil {
		return nil
	}
	parts := strings.Split(strings.TrimSpace(string(sz)), ",")
	if len(parts) != 2 {
		return nil
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	bpp := readIntFile("/sys/class/graphics/fb0/bits_per_pixel")
	stride := readIntFile("/sys/class/graphics/fb0/stride")
	if w <= 0 || h <= 0 || bpp <= 0 {
		return nil
	}
	if stride <= 0 {
		stride = w * bpp / 8
	}
	f, err := os.OpenFile("/dev/fb0", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := syscall.Mmap(int(f.Fd()), 0, stride*h,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil
	}
	return &fb{data: data, w: w, h: h, stride: stride, bpp: bpp}
}

// fillRect writes a solid grey level (r=g=b=v) so we never depend on the
// panel's channel order — track/fill/black all read correctly in any RGB order.
func (f *fb) fillRect(x, y, w, h, v int) {
	px := f.bpp / 8
	for yy := y; yy < y+h && yy < f.h; yy++ {
		if yy < 0 {
			continue
		}
		row := yy * f.stride
		for xx := x; xx < x+w && xx < f.w; xx++ {
			if xx < 0 {
				continue
			}
			o := row + xx*px
			if px == 4 {
				f.data[o] = byte(v)
				f.data[o+1] = byte(v)
				f.data[o+2] = byte(v)
				f.data[o+3] = 0xff
			} else if px == 2 {
				// RGB565 grey
				c := uint16((v>>3)<<11 | (v>>2)<<5 | (v >> 3))
				f.data[o] = byte(c)
				f.data[o+1] = byte(c >> 8)
			}
		}
	}
}

// bar geometry: bottom letterbox strip (16:9 video leaves ~96px black there)
func (f *fb) barBox() (x, y, w, h int) {
	margin := f.w / 16
	h = 14
	y = f.h - 52
	x = margin
	w = f.w - 2*margin
	return
}

func (f *fb) clearBar() {
	x, y, w, _ := f.barBox()
	f.fillRect(x-6, y-10, w+12, 40, 0x00) // black out the whole strip band
}

func (f *fb) drawBar(pos, dur float64) {
	x, y, w, h := f.barBox()
	f.fillRect(x-6, y-10, w+12, 40, 0x00) // clear band first
	f.fillRect(x, y, w, h, 0x38)          // track (dark grey)
	if dur > 0 {
		frac := pos / dur
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		fw := int(float64(w) * frac)
		f.fillRect(x, y, fw, h, 0xf0)             // filled (near-white)
		f.fillRect(x+fw-2, y-5, 4, h+10, 0xff)    // playhead knob
	}
}

// ---- main ----------------------------------------------------------------

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: ytctl <fifo> <pid> <log> [eventdev] [duration-s]")
		os.Exit(1)
	}
	fifoPath := os.Args[1]
	pid, err := strconv.Atoi(os.Args[2])
	if err != nil || pid <= 0 {
		os.Exit(1)
	}
	logPath := os.Args[3]
	dev := "/dev/input/event3"
	if len(os.Args) > 4 && os.Args[4] != "" {
		dev = os.Args[4]
	}
	duration := 0.0
	if len(os.Args) > 5 {
		duration, _ = strconv.ParseFloat(os.Args[5], 64)
	}

	ev, err := os.Open(dev)
	if err != nil {
		os.Exit(1)
	}
	fifo, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
	if err != nil {
		os.Exit(1)
	}
	screen := openFB() // nil-safe: scrub still works without the bar

	send := func(cmd string) bool {
		_, werr := fmt.Fprintf(fifo, "%s\n", cmd)
		return werr == nil
	}
	quitAndWait := func() {
		if screen != nil {
			screen.clearBar()
		}
		send("quit")
		for i := 0; i < 30 && alive(pid); i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}

	// input reader: EV_KEY presses + D-pad (EV_ABS hat-X, or key form) as
	// synthetic codes 1000 (LEFT) / 1001 (RIGHT).
	const codeLeft, codeRight = 1000, 1001
	keys := make(chan uint16, 16)
	go func() {
		buf := make([]byte, 24)
		for {
			n, rerr := ev.Read(buf)
			if rerr != nil || n < 24 {
				if rerr != nil {
					close(keys)
					return
				}
				continue
			}
			etype := binary.LittleEndian.Uint16(buf[16:18])
			code := binary.LittleEndian.Uint16(buf[18:20])
			value := int32(binary.LittleEndian.Uint32(buf[20:24]))
			switch etype {
			case 1: // EV_KEY
				if value != 1 {
					continue
				}
				switch code {
				case 105: // KEY_LEFT
					keys <- codeLeft
				case 106: // KEY_RIGHT
					keys <- codeRight
				default:
					select {
					case keys <- code:
					default:
					}
				}
			case 3: // EV_ABS — d-pad hat on this X360-clone (ABS_HAT0X = 16)
				if code == 16 {
					if value < 0 {
						keys <- codeLeft
					} else if value > 0 {
						keys <- codeRight
					}
				}
			}
		}
	}()

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
			logOff -= 64
		}
		return false
	}

	// position tracking: pos advances with wall-clock while playing; seeks jump it
	start := time.Now()
	last := time.Now()
	pos := 0.0
	playing := true

	seekTo := func(p float64) {
		if p < 0 {
			p = 0
		}
		if duration > 0 && p > duration-1 {
			p = duration - 1
		}
		pos = p
		send(fmt.Sprintf("seek to:%d", int(p)))
		if !playing {
			send("pause") // seek may resume; keep it held (pause is idempotent)
			if screen != nil {
				screen.drawBar(pos, duration)
			}
		}
	}

	tick := time.NewTicker(250 * time.Millisecond)
	for {
		select {
		case code, ok := <-keys:
			if !ok {
				keys = nil
				continue
			}
			if time.Since(start) < 600*time.Millisecond {
				continue
			}
			switch code {
			case 316: // MENU: clean stop
				quitAndWait()
				os.Exit(0)
			case 304, 305: // A/B: pause/resume toggle
				if playing {
					send("pause")
					playing = false
					if screen != nil {
						screen.drawBar(pos, duration)
					}
				} else {
					send("play")
					playing = true
					last = time.Now()
					if screen != nil {
						screen.clearBar()
					}
				}
			case codeLeft:
				seekTo(pos - 10)
			case codeRight:
				seekTo(pos + 10)
			}
		case <-tick.C:
			if !alive(pid) {
				os.Exit(2)
			}
			if playing {
				now := time.Now()
				pos += now.Sub(last).Seconds()
				last = now
			}
			if completed() {
				quitAndWait()
				os.Exit(3)
			}
		}
	}
}

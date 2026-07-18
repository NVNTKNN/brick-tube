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
	"unsafe"
)

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// ---- framebuffer progress-bar + timestamp overlay ------------------------
//
// The Brick's fb0 is heavily multi-buffered (virtual 1024x16384) and PANNED —
// the visible buffer sits at a live y-offset (seen as crop y=768 in the disp
// dump). Drawing to buffer 0 lands off-screen. So we mmap the whole virtual
// region and rebase every draw to the CURRENT pan offset read via
// FBIOGET_VSCREENINFO before each paint.

const (
	fbioGetVScreeninfo = 0x4600
	panelW             = 1024
	panelH             = 768
)

type fb struct {
	data   []byte
	fd     int
	stride int
	bpp    int
	base   int // byte offset of the visible buffer's top-left
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
	sz, err := os.ReadFile("/sys/class/graphics/fb0/virtual_size")
	if err != nil {
		return nil
	}
	parts := strings.Split(strings.TrimSpace(string(sz)), ",")
	if len(parts) != 2 {
		return nil
	}
	vh, _ := strconv.Atoi(parts[1]) // virtual height (mmap the whole region)
	bpp := readIntFile("/sys/class/graphics/fb0/bits_per_pixel")
	stride := readIntFile("/sys/class/graphics/fb0/stride")
	if vh <= 0 || bpp <= 0 {
		return nil
	}
	if stride <= 0 {
		stride = panelW * bpp / 8
	}
	f, err := os.OpenFile("/dev/fb0", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, stride*vh,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil
	}
	return &fb{data: data, fd: int(f.Fd()), stride: stride, bpp: bpp}
}

// refresh reads the live pan y-offset so draws hit the on-screen buffer.
func (f *fb) refresh() {
	var vinfo [160]byte // fb_var_screeninfo
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f.fd),
		uintptr(fbioGetVScreeninfo), uintptr(unsafe.Pointer(&vinfo[0])))
	if errno != 0 {
		return
	}
	yoffset := int(binary.LittleEndian.Uint32(vinfo[20:24])) // yoffset field
	f.base = yoffset * f.stride
}

// fillRect writes a solid grey level (r=g=b=v) — channel-order-independent.
func (f *fb) fillRect(x, y, w, h, v int) {
	px := f.bpp / 8
	for yy := y; yy < y+h && yy < panelH; yy++ {
		if yy < 0 {
			continue
		}
		row := f.base + yy*f.stride
		for xx := x; xx < x+w && xx < panelW; xx++ {
			if xx < 0 {
				continue
			}
			o := row + xx*px
			if o < 0 || o+px > len(f.data) {
				continue
			}
			if px == 4 {
				f.data[o] = byte(v)
				f.data[o+1] = byte(v)
				f.data[o+2] = byte(v)
				f.data[o+3] = 0xff
			} else if px == 2 {
				c := uint16((v>>3)<<11 | (v>>2)<<5 | (v >> 3))
				f.data[o] = byte(c)
				f.data[o+1] = byte(c >> 8)
			}
		}
	}
}

// 5x7 bitmap font, low 5 bits per row, for the timestamp glyphs.
var font5x7 = map[rune][7]uint8{
	'0': {0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E},
	'1': {0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E},
	'2': {0x0E, 0x11, 0x01, 0x02, 0x04, 0x08, 0x1F},
	'3': {0x1F, 0x02, 0x04, 0x02, 0x01, 0x11, 0x0E},
	'4': {0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02},
	'5': {0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E},
	'6': {0x06, 0x08, 0x10, 0x1E, 0x11, 0x11, 0x0E},
	'7': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8': {0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E},
	'9': {0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0C},
	':': {0x00, 0x04, 0x04, 0x00, 0x04, 0x04, 0x00},
	'/': {0x01, 0x01, 0x02, 0x04, 0x08, 0x10, 0x10},
	' ': {0, 0, 0, 0, 0, 0, 0},
}

func (f *fb) drawText(x, y, scale, v int, s string) {
	cx := x
	for _, r := range s {
		g, ok := font5x7[r]
		if !ok {
			g = font5x7[' ']
		}
		for ry := 0; ry < 7; ry++ {
			bits := g[ry]
			for rx := 0; rx < 5; rx++ {
				if bits&(1<<(4-uint(rx))) != 0 {
					f.fillRect(cx+rx*scale, y+ry*scale, scale, scale, v)
				}
			}
		}
		cx += 6 * scale // 5px glyph + 1px gap
	}
}

func fmtClock(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	t := int(sec)
	h, m, s := t/3600, (t%3600)/60, t%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// bottom-strip geometry (16:9 video leaves a ~96px black bar there)
func barBox() (x, y, w, h int) {
	margin := panelW / 16
	return margin, panelH - 44, panelW - 2*margin, 12
}

func (f *fb) clearBar() {
	f.refresh()
	f.fillRect(0, panelH-96, panelW, 96, 0x00) // black out the whole bottom band
}

func (f *fb) drawBar(pos, dur float64) {
	f.refresh()
	x, y, w, h := barBox()
	f.fillRect(0, panelH-96, panelW, 96, 0x00) // clear band first
	// timestamp above the bar: "M:SS / M:SS"
	label := fmtClock(pos)
	if dur > 0 {
		label += " / " + fmtClock(dur)
	}
	f.drawText(x, y-40, 4, 0xff, label)
	// track + fill
	f.fillRect(x, y, w, h, 0x38)
	if dur > 0 {
		frac := pos / dur
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		fw := int(float64(w) * frac)
		f.fillRect(x, y, fw, h, 0xf0)
		f.fillRect(x+fw-2, y-5, 4, h+10, 0xff)
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
		// the player's command token is "seekto" (no space) — "seek to" no-matches
		send(fmt.Sprintf("seekto:%d", int(p)))
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

package main

import (
	"encoding/binary"
	"testing"
)

func TestParseVScreeninfoSmartPro(t *testing.T) {
	var vinfo [160]byte
	binary.LittleEndian.PutUint32(vinfo[0:4], 1280)   // xres
	binary.LittleEndian.PutUint32(vinfo[4:8], 720)    // yres
	binary.LittleEndian.PutUint32(vinfo[20:24], 1440) // yoffset
	x, y, off := parseVScreeninfo(vinfo[:])
	if x != 1280 || y != 720 || off != 1440 {
		t.Fatalf("got %d,%d,%d want 1280,720,1440", x, y, off)
	}
}

func TestParseVScreeninfoShortBuffer(t *testing.T) {
	x, y, off := parseVScreeninfo(make([]byte, 8))
	if x != 0 || y != 0 || off != 0 {
		t.Fatalf("short buffer must return zeros, got %d,%d,%d", x, y, off)
	}
}

func TestClampSeek(t *testing.T) {
	cases := []struct {
		pos, delta, dur, want float64
	}{
		{10, 5, 600, 15},    // plain forward
		{10, -5, 600, 5},    // plain backward
		{2, -5, 600, 0},     // clamp at start
		{598, 5, 600, 595},  // clamp at duration-5
		{10, 5, 0, 15},      // unknown duration: no upper clamp
		{2, -5, 0, 0},       // unknown duration: lower clamp still applies
		{1, 5, 4, 0},        // shorter than 5s: max is 0
	}
	for _, c := range cases {
		if got := clampSeek(c.pos, c.delta, c.dur); got != c.want {
			t.Fatalf("clampSeek(%v,%v,%v)=%v want %v", c.pos, c.delta, c.dur, got, c.want)
		}
	}
}

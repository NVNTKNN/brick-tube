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

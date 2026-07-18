<p align="center">
  <img src="assets/logo-horizontal.png" alt="Brick Tube" width="480">
</p>

# Brick Tube

Watch YouTube on a **TrimUI Brick** handheld — untethered (Wi-Fi only, no
computer), hardware-decoded, with a thumbnail-grid browser. Search, pick, stream.

Recents / Menu → keyboard search → fast innertube search with thumbnails →
3-wide thumbnail grid → `yt-dlp` resolves a stream URL → an on-device Go
http↔https proxy → the vendor `tplayerdemo` player → Allwinner CedarX hardware
decode. A small `LD_PRELOAD` shim rewrites the display rect so 16:9 video is
letterboxed correctly on the 4:3 panel; another adds a warmth EQ for the speaker.

> **Personal hobby project. Please read [DISCLAIMER.md](DISCLAIMER.md).**
> Not affiliated with YouTube or Google. No content is hosted or distributed —
> it plays publicly available streams on your own device for personal use. You
> are responsible for complying with all applicable terms and copyright law.

## Layout

| Path | What |
|---|---|
| `pak/` | NextUI pak — `launch.sh` orchestration + `pak.json` |
| `proxy/` | `ytproxy` — Go http↔https bridge (+ TLS pre-warm) |
| `search/` | `ytsearch` — Go innertube search + thumbnail fetch |
| `minui-grid/` | 3×N thumbnail grid (fork of minui-list) |
| `ytctl/` | gamepad → player FIFO controller (pause / stop / seek) |
| `rectfix/` | `libyt_rectfix.so` — display-rect letterbox shim |
| `audiofix/` | `libyt_audiofix.so` — speaker warmth EQ shim |
| `assets/` | logo + splash + help artwork |

Build notes and the on-device deploy flow are in `HANDOFF.md`.

## Credits

Built on [minui-keyboard / minui-list / minui-presenter](https://github.com/josegonzalez)
(MIT), [yt-dlp](https://github.com/yt-dlp/yt-dlp) (Unlicense), and
[NextUI](https://github.com/LoveRetro/NextUI). MIT licensed — see [LICENSE](LICENSE).

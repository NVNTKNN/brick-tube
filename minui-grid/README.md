# minui-grid

3xN thumbnail grid picker for the YouTube pak — a fork of
[josegonzalez/minui-list](https://github.com/josegonzalez/minui-list) (same CLI
contract: `--file grid.json --item-key items --title T --write-value state`,
stdout JSON with `"selected": N`, exit 0 pick / non-0 cancel).

Items: `{"items":[{"name":"Title","thumb":"/path.jpg"}]}` — missing thumbs render
as grey cells. 3 cols x 2 visible rows; D-pad navigates, A picks, B backs out.

Build (macOS test): `PLATFORM=macos make && PLATFORM=macos make setup-resources`.
Headless render check: `MINUI_GRID_SHOT=/tmp/f.bmp ./minui-grid-macos --file ...`
dumps the first frame and exits.

Build (device): clone the MinUI union-tg5040 toolchain, drop this dir into its
workspace, `make PLATFORM=tg5040` inside the container (see repo HANDOFF).

#!/bin/sh
# Brick Tube launcher: per-device environment, then the shared core.
# Panel size feeds the rectfix letterbox shim (Brick 1024x768, Smart Pro
# 1280x720); unknown panels fall back to the Brick's.
DIR="$(dirname "$0")"
export BIN=/mnt/SDCARD/Videos
export LD_LIBRARY_PATH=/mnt/SDCARD/.system/tg5040/lib:/usr/lib:/usr/trimui/lib:$LD_LIBRARY_PATH
export PATH=/usr/trimui/bin:$PATH
PW="$(cut -d, -f1 /sys/class/graphics/fb0/virtual_size 2>/dev/null)"
case "$PW" in
  1280) PH=720 ;;
  1024) PH=768 ;;
  *)    PW=1024; PH=768 ;;
esac
export BRICKTUBE_PANEL_W="$PW" BRICKTUBE_PANEL_H="$PH"
exec /bin/sh "$DIR/bricktube-core.sh"

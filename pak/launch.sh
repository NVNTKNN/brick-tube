#!/bin/sh
# YouTube pak: keyboard search -> results list -> resolve -> on-device http/https
# proxy -> tplayerdemo (CedarX HW decode). Press any gamepad button to stop.
DIR="$(dirname "$0")"
BIN=/mnt/SDCARD/Videos          # shared binaries live here (avoid 40MB SD-card duplication)
export LD_LIBRARY_PATH=/mnt/SDCARD/.system/tg5040/lib:/usr/lib:/usr/trimui/lib:$LD_LIBRARY_PATH
export PATH=/usr/trimui/bin:$PATH
YT="$BIN/yt-dlp"; PROXY="$BIN/ytproxy"; KB="$BIN/minui-keyboard"; LIST="$BIN/minui-list"; WAIT="$BIN/waitkey"
LOG=/tmp/youtube-pak.log; : > "$LOG"
log() { echo "[yt-pak] $*" >> "$LOG"; }

echo 1 > /tmp/stay_awake
killall -9 ytproxy tplayerdemo 2>/dev/null
# keep-awake heartbeat for the session
touch /tmp/yt_pak_alive
( while [ -f /tmp/yt_pak_alive ]; do echo 1 > /tmp/stay_awake; sleep 2; done ) &

# start the http->https proxy
"$PROXY" 127.0.0.1:8888 >> "$LOG" 2>&1 &
PROXY_PID=$!
log "proxy pid $PROXY_PID"

cleanup() {
  rm -f /tmp/yt_pak_alive
  killall -9 tplayerdemo 2>/dev/null
  kill -9 "$PROXY_PID" 2>/dev/null; killall -9 ytproxy 2>/dev/null
  log "cleaned up"
}
trap cleanup EXIT INT TERM

while true; do
  # 1) search query via on-screen keyboard
  Q="$("$KB" --title "Search YouTube" --show-hardware-group)"; rc=$?
  log "keyboard rc=$rc q='$Q'"
  [ $rc -ne 0 ] && break            # Y/Menu cancel -> exit pak
  [ -z "$Q" ] && continue

  # 2) search -> id|title lines (flat = fast, no per-video extract)
  "$YT" "ytsearch12:$Q" --flat-playlist --no-warnings --print "%(id)s|%(title)s" > /tmp/yt_results.txt 2>>"$LOG"
  if [ ! -s /tmp/yt_results.txt ]; then log "no results"; continue; fi
  cut -d'|' -f2- /tmp/yt_results.txt > /tmp/yt_titles.txt
  cut -d'|' -f1  /tmp/yt_results.txt > /tmp/yt_ids.txt

  # 3) pick from the results list -> index
  STATE="$("$LIST" --format text --file /tmp/yt_titles.txt --title "$Q" --write-value state)"; rc=$?
  log "list rc=$rc state=$STATE"
  [ $rc -ne 0 ] && continue          # B/back -> new search
  IDX="$(printf %s "$STATE" | sed -n 's/.*"selected"[^0-9]*\([0-9][0-9]*\).*/\1/p' | head -1)"
  [ -z "$IDX" ] && continue
  ID="$(sed -n "$((IDX+1))p" /tmp/yt_ids.txt)"
  [ -z "$ID" ] && continue
  log "picked idx=$IDX id=$ID"

  # 4) resolve stream (prefer 720p muxed, else 360p) -> proxy target
  "$YT" -f "22/18/best[vcodec^=avc1][protocol^=https]" -g "https://www.youtube.com/watch?v=$ID" > /tmp/yt_target.txt 2>>"$LOG"
  if [ ! -s /tmp/yt_target.txt ]; then log "resolve failed"; continue; fi

  # 5) play (background) + wait for a fresh button to stop
  killall -9 tplayerdemo 2>/dev/null
  tplayerdemo http://127.0.0.1:8888/stream.mp4 >> "$LOG" 2>&1 &
  TPID=$!
  log "playing pid $TPID"
  sleep 1
  "$WAIT" /dev/input/event3          # blocks until a gamepad button press
  kill -9 "$TPID" 2>/dev/null; killall -9 tplayerdemo 2>/dev/null
  log "stopped"
  sleep 1
done
cleanup

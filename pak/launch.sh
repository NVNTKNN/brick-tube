#!/bin/sh
# YouTube pak: keyboard search -> results list -> resolve -> on-device http/https
# proxy -> tplayerdemo (CedarX HW decode), driven via a stdin FIFO so stop is a
# clean "quit" (video layer torn down — no grey screen). MENU stops, A/B pause,
# volume never stops playback. Stopping a video returns to the results list.
DIR="$(dirname "$0")"
BIN=/mnt/SDCARD/Videos          # shared binaries live here (avoid 40MB SD-card duplication)
export LD_LIBRARY_PATH=/mnt/SDCARD/.system/tg5040/lib:/usr/lib:/usr/trimui/lib:$LD_LIBRARY_PATH
export PATH=/usr/trimui/bin:$PATH
YT="$BIN/yt-dlp"; PROXY="$BIN/ytproxy"; KB="$BIN/minui-keyboard"; LIST="$BIN/minui-list"
CTL="$BIN/ytctl"; MSG="$BIN/minui-presenter"
LOG=/tmp/youtube-pak.log; : > "$LOG"
FIFO=/tmp/yt_ctl
log() { echo "[yt-pak] $*" >> "$LOG"; }

killall -9 ytproxy tplayerdemo minui-presenter 2>/dev/null

# start the http->https proxy
"$PROXY" 127.0.0.1:8888 >> "$LOG" 2>&1 &
PROXY_PID=$!
log "proxy pid $PROXY_PID"

TPID=""
AWAKE_PID=""
cleanup() {
  rm -f /tmp/yt_play_alive
  [ -n "$AWAKE_PID" ] && kill "$AWAKE_PID" 2>/dev/null
  killall minui-presenter 2>/dev/null
  killall tplayerdemo 2>/dev/null; sleep 1; killall -9 tplayerdemo 2>/dev/null
  kill -9 "$PROXY_PID" 2>/dev/null; killall -9 ytproxy 2>/dev/null
  rm -f "$FIFO"
  log "cleaned up"
}
trap cleanup EXIT INT TERM

# --- status screens (minui-presenter) ---
busy()    { killall minui-presenter 2>/dev/null; "$MSG" --message "$1" --timeout -1 >>"$LOG" 2>&1 & }
notice()  { killall minui-presenter 2>/dev/null; "$MSG" --message "$1" --timeout "${2:-3}" >>"$LOG" 2>&1; }
busy_off() { killall minui-presenter 2>/dev/null; }

# Watch the log tail (from line $1) for playback start/failure. 0=playing 1=failed
play_ok() {
  i=0
  while [ $i -lt 40 ]; do                      # ~16s budget for prepare
    kill -0 "$TPID" 2>/dev/null || return 1
    case "$(tail -n +"$1" "$LOG")" in
      *TPLAYER_NOTIFY_MEDIA_ERROR*|*"can not play this file"*|*"prepare fail"*|*"open media source fail"*) return 1 ;;
      *"start play"*|*"video decoded"*) return 0 ;;
    esac
    sleep 0.4; i=$((i+1))
  done
  return 1
}

# graceful stop ladder: FIFO quit -> SIGTERM (has a handler) -> kill -9 (last resort)
stop_player() {
  [ -n "$TPID" ] || return 0
  if kill -0 "$TPID" 2>/dev/null; then
    ( echo quit >&3 ) 2>/dev/null              # subshell: survive SIGPIPE if reader died
    i=0; while kill -0 "$TPID" 2>/dev/null && [ $i -lt 10 ]; do sleep 0.3; i=$((i+1)); done
  fi
  if kill -0 "$TPID" 2>/dev/null; then
    log "quit ignored; SIGTERM"
    kill "$TPID" 2>/dev/null
    i=0; while kill -0 "$TPID" 2>/dev/null && [ $i -lt 10 ]; do sleep 0.3; i=$((i+1)); done
  fi
  if kill -0 "$TPID" 2>/dev/null; then
    log "SIGTERM ignored; kill -9 (video layer may stick)"
    kill -9 "$TPID" 2>/dev/null
  fi
  wait "$TPID" 2>/dev/null
  TPID=""
}

# $1 = proxy URL. 0 = played (however it ended), 1 = never started.
play_video() {
  MARK=$(( $(wc -l < "$LOG") + 1 ))
  rm -f "$FIFO"; mkfifo "$FIFO"
  tplayerdemo "$1" < "$FIFO" >> "$LOG" 2>&1 &
  TPID=$!
  exec 3> "$FIFO"                              # hold the writer so stdin never EOFs
  log "playing pid $TPID"
  if ! play_ok "$MARK"; then
    log "playback failed to start"
    stop_player
    exec 3>&-; rm -f "$FIFO"
    return 1
  fi
  busy_off                                     # video layer is up; drop the Loading page
  # keep-awake scoped to playback only (battery)
  touch /tmp/yt_play_alive
  ( while [ -f /tmp/yt_play_alive ]; do echo 1 > /tmp/stay_awake; sleep 2; done ) &
  AWAKE_PID=$!
  "$CTL" "$FIFO" "$TPID" "$LOG" /dev/input/event3
  log "ytctl rc=$?"
  rm -f /tmp/yt_play_alive; kill "$AWAKE_PID" 2>/dev/null; AWAKE_PID=""
  stop_player
  exec 3>&-; rm -f "$FIFO"
  return 0
}

LASTQ=""
while true; do
  # 1) search query via on-screen keyboard (prefilled with the last query)
  if [ -n "$LASTQ" ]; then
    Q="$("$KB" --title "Search YouTube" --initial-value "$LASTQ" --show-hardware-group)"; rc=$?
  else
    Q="$("$KB" --title "Search YouTube" --show-hardware-group)"; rc=$?
  fi
  log "keyboard rc=$rc q='$Q'"
  [ $rc -ne 0 ] && break            # Y/Menu cancel -> exit pak
  [ -z "$Q" ] && continue
  LASTQ="$Q"

  # 2) search -> id|duration|title lines (flat = fast, no per-video extract)
  busy "Searching: $Q"
  "$YT" "ytsearch12:$Q" --flat-playlist --no-warnings --print "%(id)s|%(duration_string|)s|%(title).80s" > /tmp/yt_results.txt 2>>"$LOG"
  if [ ! -s /tmp/yt_results.txt ]; then
    log "no results"
    notice "No results — check WiFi?" 3
    continue
  fi
  cut -d'|' -f1 /tmp/yt_results.txt > /tmp/yt_ids.txt
  awk -F'|' '{t=$3; for(i=4;i<=NF;i++) t=t"|"$i; if($2!="") t=t"  ["$2"]"; print t}' \
    /tmp/yt_results.txt > /tmp/yt_titles.txt
  busy_off

  # 3) results list; after a video stops we come back here, not the keyboard
  while true; do
    STATE="$("$LIST" --format text --file /tmp/yt_titles.txt --title "$Q" --write-value state)"; rc=$?
    log "list rc=$rc state=$STATE"
    [ $rc -ne 0 ] && break           # B/back -> new search
    IDX="$(printf %s "$STATE" | sed -n 's/.*"selected"[^0-9]*\([0-9][0-9]*\).*/\1/p' | head -1)"
    [ -z "$IDX" ] && break
    ID="$(sed -n "$((IDX+1))p" /tmp/yt_ids.txt)"
    [ -z "$ID" ] && break
    log "picked idx=$IDX id=$ID"

    # 4) resolve stream (prefer 720p muxed when it exists, else 360p) -> proxy target
    busy "Loading video..."
    "$YT" -f "22/18/best[vcodec^=avc1][protocol^=https]" -g "https://www.youtube.com/watch?v=$ID" > /tmp/yt_target.txt 2>>"$LOG"
    if [ ! -s /tmp/yt_target.txt ]; then
      log "resolve failed"
      notice "Could not load this video" 3
      continue
    fi

    # 5) play; MENU = clean stop -> back to this list. A/B pause. Volume free.
    if ! play_video "http://127.0.0.1:8888/stream.mp4"; then
      notice "Playback failed — try another video" 3
    fi
  done
done
cleanup

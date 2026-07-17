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
CTL="$BIN/ytctl"; MSG="$BIN/minui-presenter"; SEARCH="$BIN/ytsearch"; GRIDBIN="$BIN/minui-grid"
LOG=/tmp/youtube-pak.log; : > "$LOG"
FIFO=/tmp/yt_ctl
SCREEN_W=1024; SCREEN_H=768                    # Brick panel (4:3) — why 16:9 video stretched
HIST=/mnt/SDCARD/Videos/yt_history.txt         # recent searches, MRU-first, cap 10
log() { echo "[yt-pak] $*" >> "$LOG"; }

killall -9 ytproxy tplayerdemo minui-presenter 2>/dev/null

# start the http->https proxy
"$PROXY" 127.0.0.1:8888 >> "$LOG" 2>&1 &
PROXY_PID=$!
log "proxy pid $PROXY_PID"

TPID=""
AWAKE_PID=""
cleanup() {
  set_gov "$GOV_SAVE"; wifi_ps on
  rm -f /tmp/yt_play_alive
  [ -n "$AWAKE_PID" ] && kill "$AWAKE_PID" 2>/dev/null
  killall minui-presenter 2>/dev/null
  killall tplayerdemo 2>/dev/null; sleep 1; killall -9 tplayerdemo 2>/dev/null
  kill -9 "$PROXY_PID" 2>/dev/null; killall -9 ytproxy 2>/dev/null
  rm -f "$FIFO"
  log "cleaned up"
}
trap cleanup EXIT INT TERM

# --- A133 hardware knobs (write-if-exists; saved + restored on exit) ---
# Measured 2026-07-17: governor=schedutil (performance/conservative available);
# iw present; tcp_rmem max already 6MB (left alone); yt-dlp startup is
# CPU-bound (4.5s warm=cold) so tmpfs copy is useless — governor burst helps.
GOV_SAVE="$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor 2>/dev/null)"
set_gov() {
  [ -n "$GOV_SAVE" ] || return 0
  for g in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do
    echo "$1" > "$g" 2>/dev/null
  done
}
wifi_ps() { /usr/sbin/iw dev wlan0 set power_save "$1" 2>/dev/null; }

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
  # playback profile: decode is on the VPU — drop CPU clocks, kill WiFi power-save
  # (PS-mode batching = the classic stream-stutter cause)
  set_gov conservative
  wifi_ps off
  # Aspect-correct letterbox: the demo sets the display rect to the VIDEO size and
  # the disp layer stretches to the 4:3 panel. Parse the decoded WxH from the log
  # and override with a fitted rect over the same FIFO ("set dst_rect: x y w h").
  VS="$(tail -n +"$MARK" "$LOG" | sed -n 's/.*video decoded width = \([0-9][0-9]*\),height = \([0-9][0-9]*\).*/\1 \2/p' | head -1)"
  if [ -n "$VS" ]; then
    set -- $VS; VW=$1; VH=$2
    if [ "$VW" -gt 0 ] && [ "$VH" -gt 0 ]; then
      DW=$SCREEN_W; DH=$((SCREEN_W*VH/VW))
      if [ "$DH" -gt "$SCREEN_H" ]; then DH=$SCREEN_H; DW=$((SCREEN_H*VW/VH)); fi
      DW=$((DW/2*2)); DH=$((DH/2*2))           # even-align for the disp driver
      DX=$(( (SCREEN_W-DW)/2 )); DY=$(( (SCREEN_H-DH)/2 ))
      sleep 0.3                                # let the demo's own rect land first
      ( echo "set dst_rect: $DX $DY $DW $DH" >&3 ) 2>/dev/null
      log "letterbox dst_rect: $DX $DY $DW $DH (video ${VW}x${VH})"
    fi
  fi
  # keep-awake scoped to playback only (battery)
  touch /tmp/yt_play_alive
  ( while [ -f /tmp/yt_play_alive ]; do echo 1 > /tmp/stay_awake; sleep 2; done ) &
  AWAKE_PID=$!
  "$CTL" "$FIFO" "$TPID" "$LOG" /dev/input/event3
  log "ytctl rc=$?"
  rm -f /tmp/yt_play_alive; kill "$AWAKE_PID" 2>/dev/null; AWAKE_PID=""
  set_gov "$GOV_SAVE"
  wifi_ps on
  stop_player
  exec 3>&-; rm -f "$FIFO"
  return 0
}

LASTQ=""
while true; do
  # 0) recents screen: pick a past query or start a new search
  { echo "> New search..."; [ -f "$HIST" ] && head -10 "$HIST"; } > /tmp/yt_recents.txt
  STATE="$("$LIST" --format text --file /tmp/yt_recents.txt --title "YouTube" --write-value state)"; rc=$?
  log "recents rc=$rc state=$STATE"
  [ $rc -ne 0 ] && break            # cancel from recents -> exit pak
  RIDX="$(printf %s "$STATE" | sed -n 's/.*"selected"[^0-9]*\([0-9][0-9]*\).*/\1/p' | head -1)"
  [ -z "$RIDX" ] && break
  if [ "$RIDX" -eq 0 ]; then
    # 1) new search via on-screen keyboard (prefilled with the last query)
    if [ -n "$LASTQ" ]; then
      Q="$("$KB" --title "Search YouTube" --initial-value "$LASTQ" --show-hardware-group)"; rc=$?
    else
      Q="$("$KB" --title "Search YouTube" --show-hardware-group)"; rc=$?
    fi
    log "keyboard rc=$rc q='$Q'"
    [ $rc -ne 0 ] && continue       # keyboard cancel -> back to recents
    [ -z "$Q" ] && continue
  else
    Q="$(sed -n "${RIDX}p" "$HIST")"  # list row N = history line N (row 0 is New search)
    [ -z "$Q" ] && continue
    log "recent pick '$Q'"
  fi
  LASTQ="$Q"

  # 2) search -> id|duration|title lines. Fast path: ytsearch (Go innertube,
  # ~1s + thumbnails for the grid). Fallback: yt-dlp flat search (proven v1).
  busy "Searching: $Q"
  set_gov performance
  T0=$(date +%s)
  : > /tmp/yt_results.txt
  GRID_OK=0
  if [ -x "$SEARCH" ] && "$SEARCH" "$Q" 12 /tmp/yt_results.txt /tmp/yt_thumbs /tmp >>"$LOG" 2>&1 && [ -s /tmp/yt_results.txt ]; then
    GRID_OK=1
    log "timing search-fast $(( $(date +%s) - T0 ))s"
  else
    log "ytsearch unavailable/failed -> yt-dlp search"
    "$YT" "ytsearch12:$Q" --flat-playlist --no-warnings --print "%(id)s|%(duration_string|)s|%(title).80s" > /tmp/yt_results.txt 2>>"$LOG"
    log "timing search-fallback $(( $(date +%s) - T0 ))s"
  fi
  set_gov "$GOV_SAVE"
  if [ ! -s /tmp/yt_results.txt ]; then
    log "no results"
    notice "No results — check WiFi?" 3
    continue
  fi
  # remember the query (MRU, deduped, cap 10)
  { echo "$Q"; grep -Fxv -- "$Q" "$HIST" 2>/dev/null; } | head -10 > /tmp/yt_hist.new \
    && cp /tmp/yt_hist.new "$HIST"
  cut -d'|' -f1 /tmp/yt_results.txt > /tmp/yt_ids.txt
  awk -F'|' '{t=$3; for(i=4;i<=NF;i++) t=t"|"$i; if($2!="") t=t"  ["$2"]"; print t}' \
    /tmp/yt_results.txt > /tmp/yt_titles.txt
  busy_off

  # 3) browse; after a video stops we come back here, not the keyboard.
  # Thumbnail grid when ytsearch produced grid.json + the binary exists;
  # abnormal grid exit (crash/error) falls back to the text list for good.
  while true; do
    if [ "$GRID_OK" = 1 ] && [ -x "$GRIDBIN" ] && [ -s /tmp/grid.json ]; then
      STATE="$("$GRIDBIN" --file /tmp/grid.json --item-key items --title "$Q" --write-value state)"; rc=$?
      case $rc in
        0|2|3) ;;                    # pick / B-back / MENU-back = normal
        *) log "minui-grid rc=$rc -> text list fallback"; GRID_OK=0
           STATE="$("$LIST" --format text --file /tmp/yt_titles.txt --title "$Q" --write-value state)"; rc=$? ;;
      esac
    else
      STATE="$("$LIST" --format text --file /tmp/yt_titles.txt --title "$Q" --write-value state)"; rc=$?
    fi
    log "list rc=$rc state=$STATE"
    [ $rc -ne 0 ] && break           # B/back -> new search
    IDX="$(printf %s "$STATE" | sed -n 's/.*"selected"[^0-9]*\([0-9][0-9]*\).*/\1/p' | head -1)"
    [ -z "$IDX" ] && break
    ID="$(sed -n "$((IDX+1))p" /tmp/yt_ids.txt)"
    [ -z "$ID" ] && break
    log "picked idx=$IDX id=$ID"

    # 4) resolve stream (prefer 720p muxed when it exists, else 360p) -> proxy target
    busy "Loading video..."
    set_gov performance
    T0=$(date +%s)
    "$YT" -f "22/18/best[vcodec^=avc1][protocol^=https]" -g "https://www.youtube.com/watch?v=$ID" > /tmp/yt_target.txt 2>>"$LOG"
    log "timing resolve $(( $(date +%s) - T0 ))s"
    set_gov "$GOV_SAVE"
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

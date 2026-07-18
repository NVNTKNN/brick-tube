#!/bin/sh
# Brick Tube pak: keyboard search -> results list -> resolve -> on-device http/https
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
HIST=/mnt/SDCARD/Videos/yt_history.txt         # recent searches, MRU-first, cap 10
log() { echo "[yt-pak] $*" >> "$LOG"; }

killall -9 ytproxy tplayerdemo minui-presenter 2>/dev/null

# start the http->https proxy
"$PROXY" 127.0.0.1:8888 >> "$LOG" 2>&1 &
PROXY_PID=$!
log "proxy pid $PROXY_PID"
log "battery at start: $(batt)%"

TPID=""
AWAKE_PID=""
cleanup() {
  set_gov "$GOV_SAVE"; wifi_ps on; set_maxf "$MAXF_SAVE"
  log "battery at exit: $(batt)%" 
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
MAXF_SAVE="$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq 2>/dev/null)"
set_maxf() {  # cap CPU clock (playback needs ~none of it — the VPU decodes)
  [ -n "$MAXF_SAVE" ] || return 0
  for g in /sys/devices/system/cpu/cpu*/cpufreq/scaling_max_freq; do
    echo "$1" > "$g" 2>/dev/null
  done
}
batt() { cat /sys/class/power_supply/axp2202-battery/capacity 2>/dev/null; }

# --- status screens (minui-presenter) ---
busy()    { killall minui-presenter 2>/dev/null; "$MSG" --message "$1" --timeout -1 >>"$LOG" 2>&1 & }
notice()  { killall minui-presenter 2>/dev/null; "$MSG" --message "$1" --timeout "${2:-3}" >>"$LOG" 2>&1; }
busy_off() { killall minui-presenter 2>/dev/null; }

# splash screen on load (Brick Tube logo, ~2s)
[ -f "$DIR/splash.png" ] && "$MSG" --message "" --background-image "$DIR/splash.png" --timeout 2 >>"$LOG" 2>&1

# --- menu (reachable from the recents screen) ---
# parse a minui-list "{"selected": N}" state blob -> N
menu_idx() { printf %s "$1" | sed -n 's/.*"selected"[^0-9]*\([0-9][0-9]*\).*/\1/p' | head -1; }

show_help() {
  if [ -f "$DIR/help.png" ]; then
    "$MSG" --timeout 0 --confirm-button B --confirm-text "BACK" --confirm-show \
      --message "" --background-image "$DIR/help.png" >>"$LOG" 2>&1
  else
    "$MSG" --timeout 0 --confirm-button B --confirm-text "BACK" --confirm-show \
      --message "A/B pause, LEFT/RIGHT seek, MENU stop, B back" >>"$LOG" 2>&1
  fi
}

show_about() {
  "$MSG" --timeout 0 --confirm-button B --confirm-text "BACK" --confirm-show \
    --message "BRICK TUBE   v0.1.0

Watch video on your TrimUI Brick --
hardware-decoded and untethered.

For personal use. Not affiliated with
YouTube or Google. Plays public streams
on your device; hosts no content.

Made by navneeth
github.com/NVNTKNN/brick-tube" >>"$LOG" 2>&1
}

clear_recents() {
  printf '%s\n' "No, keep them" "Yes, clear them" > /tmp/yt_confirm.txt
  S="$("$LIST" --format text --file /tmp/yt_confirm.txt --title "Clear recent searches?" --write-value state)"; rc=$?
  [ $rc -ne 0 ] && return
  [ "$(menu_idx "$S")" = "1" ] && { rm -f "$HIST"; notice "Recent searches cleared" 2; }
}

settings_menu() {
  while true; do
    printf '%s\n' "How to use" "About" "Clear recent searches" > /tmp/yt_menu.txt
    S="$("$LIST" --format text --file /tmp/yt_menu.txt --title "Menu" --write-value state)"; rc=$?
    [ $rc -ne 0 ] && return          # B/back -> recents
    case "$(menu_idx "$S")" in
      0) show_help ;;
      1) show_about ;;
      2) clear_recents ;;
      *) return ;;
    esac
  done
}

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
  # rectfix shim: rewrites the demo's TPlayerSetDisplayRect(video WxH) into an
  # aspect-fitted centered rect — the stdin "set dst_rect" parser mangles values
  T0=$(date +%s)
  LD_PRELOAD="/mnt/SDCARD/Videos/libyt_rectfix.so /mnt/SDCARD/Videos/libyt_audiofix.so" tplayerdemo "$1" < "$FIFO" >> "$LOG" 2>&1 &
  TPID=$!
  exec 3> "$FIFO"                              # hold the writer so stdin never EOFs
  log "playing pid $TPID"
  if ! play_ok "$MARK"; then
    log "playback failed to start"
    stop_player
    exec 3>&-; rm -f "$FIFO"
    return 1
  fi
  log "timing prepare $(( $(date +%s) - T0 ))s"  # spawn -> first frames/start play
  busy_off                                     # video layer is up; drop the Loading page
  # playback profile: decode is on the VPU — drop CPU clocks, kill WiFi power-save
  # (PS-mode batching = the classic stream-stutter cause)
  set_gov conservative
  set_maxf 1008000
  wifi_ps off
  log "battery at play start: $(batt)%" 
  # keep-awake scoped to playback only (battery)
  touch /tmp/yt_play_alive
  ( while [ -f /tmp/yt_play_alive ]; do echo 1 > /tmp/stay_awake; sleep 2; done ) &
  AWAKE_PID=$!
  "$CTL" "$FIFO" "$TPID" "$LOG" /dev/input/event3 "$DUR_S"
  log "ytctl rc=$?"
  rm -f /tmp/yt_play_alive; kill "$AWAKE_PID" 2>/dev/null; AWAKE_PID=""
  set_gov "$GOV_SAVE"
  set_maxf "$MAXF_SAVE"
  wifi_ps on
  log "battery at play end: $(batt)%" 
  stop_player
  exec 3>&-; rm -f "$FIFO"
  return 0
}

# fast yt-dlp: onedir build unpacked to tmpfs once per boot. The onefile binary
# pays ~4.5s of LZMA self-extraction on EVERY run (measured, CPU-bound); the
# unpacked tree boots in ~1-2s. Falls through to the onefile binary if absent.
YTFAST=/tmp/ytdlp/yt-dlp/yt-dlp
if [ ! -x "$YTFAST" ] && [ -f "$BIN/ytdlp-onedir.tgz" ]; then
  busy "Preparing (once per boot)..."
  mkdir -p /tmp/ytdlp && tar xzf "$BIN/ytdlp-onedir.tgz" -C /tmp/ytdlp 2>>"$LOG" \
    && log "onedir yt-dlp unpacked to tmpfs"
  busy_off
fi
# adopt only if it actually runs (a glibc-mismatched build is -x but useless)
if [ -x "$YTFAST" ] && "$YTFAST" --version >/dev/null 2>&1; then
  YT="$YTFAST"; log "using onedir yt-dlp"
fi

LASTQ=""
while true; do
  # 0) recents screen: New search / Menu / past queries
  { echo "> New search..."; echo "= Menu ="; [ -f "$HIST" ] && head -10 "$HIST"; } > /tmp/yt_recents.txt
  STATE="$("$LIST" --format text --file /tmp/yt_recents.txt --title "Brick Tube" --write-value state)"; rc=$?
  log "recents rc=$rc state=$STATE"
  [ $rc -ne 0 ] && break            # cancel from recents -> exit pak
  RIDX="$(menu_idx "$STATE")"
  [ -z "$RIDX" ] && break
  if [ "$RIDX" -eq 0 ]; then
    # new search via on-screen keyboard (prefilled with the last query)
    if [ -n "$LASTQ" ]; then
      Q="$("$KB" --title "Search" --initial-value "$LASTQ" --show-hardware-group)"; rc=$?
    else
      Q="$("$KB" --title "Search" --show-hardware-group)"; rc=$?
    fi
    log "keyboard rc=$rc q='$Q'"
    [ $rc -ne 0 ] && continue       # keyboard cancel -> back to recents
    [ -z "$Q" ] && continue
  elif [ "$RIDX" -eq 1 ]; then
    settings_menu                   # row 1 = Menu
    continue
  else
    Q="$(sed -n "$((RIDX-1))p" "$HIST")"  # rows 0,1 = New search, Menu; history starts at row 2
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
    "$YT" --no-check-certificates "ytsearch12:$Q" --flat-playlist --no-warnings --print "%(id)s|%(duration_string|)s|%(title).80s" > /tmp/yt_results.txt 2>>"$LOG"
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
      STATE="$("$GRIDBIN" --file /tmp/grid.json --item-key items --title "" --write-value state)"; rc=$?
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
    # duration seconds from the results row (field 2, "M:SS" or "H:MM:SS"); 0 if unknown
    DUR_STR="$(sed -n "$((IDX+1))p" /tmp/yt_results.txt | cut -d'|' -f2)"
    DUR_S="$(echo "$DUR_STR" | awk -F: '{n=NF; s=0; for(i=1;i<=n;i++) s=s*60+$i; print s+0}')"
    log "picked idx=$IDX id=$ID dur=${DUR_S}s"

    # 4) resolve stream (prefer 720p muxed when it exists, else 360p) -> proxy target
    busy "Loading video..."
    set_gov performance
    T0=$(date +%s)
    # android client first: dodges the web client's bot-check/429 on flagged IPs
    # (verified 2026-07-17 from home IP: web=bot-check, android=clean URL) and
    # needs no JS runtime. Default client is the retry if android comes up empty.
    "$YT" --no-check-certificates -f "22/18/best[vcodec^=avc1][protocol^=https]" -g --extractor-args "youtube:player_client=android;player_skip=webpage" "https://www.youtube.com/watch?v=$ID" > /tmp/yt_target.txt 2>>"$LOG"
    if [ ! -s /tmp/yt_target.txt ]; then
      log "android client resolve empty -> default client retry"
      "$YT" --no-check-certificates -f "22/18/best[vcodec^=avc1][protocol^=https]" -g "https://www.youtube.com/watch?v=$ID" > /tmp/yt_target.txt 2>>"$LOG"
    fi
    log "timing resolve $(( $(date +%s) - T0 ))s"
    set_gov "$GOV_SAVE"
    if [ ! -s /tmp/yt_target.txt ]; then
      log "resolve failed"
      notice "Could not load this video" 3
      continue
    fi

    # warm the proxy's googlevideo TLS connection while tplayerdemo spawns
    wget -q -O /dev/null http://127.0.0.1:8888/warm 2>/dev/null &

    # 5) play; MENU = clean stop -> back to this list. A/B pause. Volume free.
    if ! play_video "http://127.0.0.1:8888/stream.mp4"; then
      notice "Playback failed — try another video" 3
    fi
  done
done
cleanup

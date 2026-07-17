// ytproxy: on-device http->https bridge for tplayerdemo (Tina stream client is
// http-only). Serves plain http on 0.0.0.0:8888; forwards each request to the
// https target URL in /tmp/yt_target.txt, passing Range through so mp4 seeking
// works. Static aarch64 binary (CGO off) — runs on TinaLinux glibc 2.33.
package main

import (
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const targetFile = "/tmp/yt_target.txt"

// TinaLinux has no CA bundle, so Go can't verify googlevideo's cert. Skip
// verification — we're bridging the user's own yt-dlp-resolved URL on their LAN,
// not authenticating anything. (The URL is already trusted; MITM risk is nil here.)
var client = &http.Client{
	Timeout: 0, // no overall timeout: long streams
	Transport: &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
	},
}

// warm: open the TLS connection to the target host now (handshakes cost ~1-2s
// on the A53s) so the player's first real request reuses a pooled connection.
func warm(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(targetFile)
	if err != nil {
		http.Error(w, "no target", http.StatusServiceUnavailable)
		return
	}
	req, err := http.NewRequest("GET", strings.TrimSpace(string(b)), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	w.WriteHeader(http.StatusNoContent)
}

func handler(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(targetFile)
	if err != nil {
		http.Error(w, "no target", http.StatusServiceUnavailable)
		return
	}
	target := strings.TrimSpace(string(b))
	if target == "" {
		http.Error(w, "empty target", http.StatusServiceUnavailable)
		return
	}
	req, err := http.NewRequest(r.Method, target, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	// 256KB chunks (vs io.Copy's 32KB): fewer syscalls and smoother delivery
	// into the decoder across WiFi jitter on the A53 cores.
	buf := make([]byte, 256*1024)
	io.CopyBuffer(w, resp.Body, buf)
}

func main() {
	addr := "0.0.0.0:8888"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	http.HandleFunc("/warm", warm)
	http.HandleFunc("/", handler)
	srv := &http.Server{Addr: addr, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("ytproxy listening on %s -> %s", addr, targetFile)
	log.Fatal(srv.ListenAndServe())
}

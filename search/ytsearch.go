// ytsearch: fast YouTube search for the pak via the innertube API — one HTTPS
// POST (~1s) instead of yt-dlp's ~5s PyInstaller boot + search. Also downloads
// result thumbnails concurrently into tmpfs for minui-grid.
//
// usage: ytsearch <query> <max> <results_path> <thumbdir> <griddir>
//   results_path : gets "id|duration|title" lines (same shape yt-dlp search
//                  produces in launch.sh — the v1 fallback stays drop-in)
//   thumbdir     : gets <id>.jpg mqdefault thumbnails (failures non-fatal)
//   griddir      : gets grid.json {"items":[{"name":"Title  [dur]","thumb":path}]}
// exit 0 with >=1 result, non-zero otherwise (launch.sh falls back to yt-dlp).
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// TinaLinux has no CA bundle (same constraint as ytproxy) — skip verification.
var client = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
}

type result struct {
	id       string
	title    string
	duration string
}

// walk the decoded JSON for every "videoRenderer" object, in document order
func collect(v any, out *[]result) {
	switch t := v.(type) {
	case map[string]any:
		if vr, ok := t["videoRenderer"].(map[string]any); ok {
			r := result{}
			r.id, _ = vr["videoId"].(string)
			if title, ok := vr["title"].(map[string]any); ok {
				if runs, ok := title["runs"].([]any); ok && len(runs) > 0 {
					if run, ok := runs[0].(map[string]any); ok {
						r.title, _ = run["text"].(string)
					}
				}
			}
			if lt, ok := vr["lengthText"].(map[string]any); ok {
				r.duration, _ = lt["simpleText"].(string) // absent on live streams
			}
			if r.id != "" && r.title != "" {
				*out = append(*out, r)
			}
		}
		for _, val := range t {
			collect(val, out)
		}
	case []any:
		for _, val := range t {
			collect(val, out)
		}
	}
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func main() {
	// child mode: download thumbnails detached from the pak flow
	if len(os.Args) > 2 && os.Args[1] == "-thumbs" {
		downloadThumbs(os.Args[2], os.Args[3:])
		return
	}
	if len(os.Args) < 6 {
		fmt.Fprintln(os.Stderr, "usage: ytsearch <query> <max> <results_path> <thumbdir> <griddir>")
		os.Exit(1)
	}
	query := os.Args[1]
	max, err := strconv.Atoi(os.Args[2])
	if err != nil || max <= 0 {
		max = 12
	}
	resultsPath, thumbDir, gridDir := os.Args[3], os.Args[4], os.Args[5]

	body, _ := json.Marshal(map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    "WEB",
				"clientVersion": "2.20240101.00.00",
			},
		},
		"query": query,
	})
	resp, err := client.Post(
		"https://www.youtube.com/youtubei/v1/search?prettyPrint=false",
		"application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "search request failed:", err)
		os.Exit(2)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Fprintln(os.Stderr, "search http status", resp.StatusCode)
		os.Exit(2)
	}
	var doc any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&doc); err != nil {
		fmt.Fprintln(os.Stderr, "decode failed:", err)
		os.Exit(2)
	}

	var results []result
	collect(doc, &results)
	// dedupe by id, keep order, cap at max
	seen := map[string]bool{}
	uniq := results[:0]
	for _, r := range results {
		if !seen[r.id] {
			seen[r.id] = true
			uniq = append(uniq, r)
		}
		if len(uniq) >= max {
			break
		}
	}
	results = uniq
	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "no results parsed")
		os.Exit(3)
	}

	// results file: id|duration|title (title truncated like the yt-dlp template)
	var lines strings.Builder
	for _, r := range results {
		lines.WriteString(r.id + "|" + r.duration + "|" + truncate(r.title, 80) + "\n")
	}
	if err := os.WriteFile(resultsPath, []byte(lines.String()), 0644); err != nil {
		fmt.Fprintln(os.Stderr, "write results:", err)
		os.Exit(2)
	}

	// grid.json for minui-grid
	type gridItem struct {
		Name  string `json:"name"`
		Thumb string `json:"thumb"`
	}
	items := make([]gridItem, 0, len(results))
	for _, r := range results {
		name := truncate(r.title, 80)
		if r.duration != "" {
			name += "  [" + r.duration + "]"
		}
		items = append(items, gridItem{Name: name, Thumb: filepath.Join(thumbDir, r.id+".jpg")})
	}
	gridJSON, _ := json.Marshal(map[string]any{"items": items})
	if err := os.WriteFile(filepath.Join(gridDir, "grid.json"), gridJSON, 0644); err != nil {
		fmt.Fprintln(os.Stderr, "write grid.json:", err)
		os.Exit(2)
	}

	// thumbnails: hand off to a detached child so the pak can open the grid
	// immediately — minui-grid fills cells in as files land in tmpfs.
	os.MkdirAll(thumbDir, 0755)
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.id)
	}
	if exe, err := os.Executable(); err == nil {
		child := exec.Command(exe, append([]string{"-thumbs", thumbDir}, ids...)...)
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := child.Start(); err != nil {
			downloadThumbs(thumbDir, ids) // can't detach -> do it inline
		}
	} else {
		downloadThumbs(thumbDir, ids)
	}
	fmt.Printf("%d results\n", len(results))
}

// downloadThumbs fetches mqdefault thumbnails with bounded concurrency — a TLS
// handshake costs real CPU on the A53s, so 12 at once contend and time out.
func downloadThumbs(thumbDir string, ids []string) {
	thumbClient := &http.Client{Timeout: 15 * time.Second, Transport: client.Transport}
	jobs := make(chan string, len(ids))
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				dst := filepath.Join(thumbDir, id+".jpg")
				if _, err := os.Stat(dst); err == nil {
					continue // cached from an earlier search this session
				}
				resp, err := thumbClient.Get("https://i.ytimg.com/vi/" + id + "/mqdefault.jpg")
				if err != nil {
					continue
				}
				if resp.StatusCode != 200 {
					resp.Body.Close()
					continue
				}
				data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
				resp.Body.Close()
				if err == nil && len(data) > 0 {
					tmp := dst + ".part"
					if os.WriteFile(tmp, data, 0644) == nil {
						os.Rename(tmp, dst) // atomic: grid never loads a half-written jpg
					}
				}
			}
		}()
	}
	wg.Wait()
}

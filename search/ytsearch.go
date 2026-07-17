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
	"strconv"
	"strings"
	"sync"
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

	// thumbnails: bounded concurrency — a TLS handshake costs real CPU on the
	// A53s, so 12 at once contend and time out; 4 workers reuse connections.
	os.MkdirAll(thumbDir, 0755)
	thumbClient := &http.Client{Timeout: 15 * time.Second, Transport: client.Transport}
	jobs := make(chan string, len(results))
	for _, r := range results {
		jobs <- r.id
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
					fmt.Fprintln(os.Stderr, "thumb", id, err)
					continue
				}
				if resp.StatusCode != 200 {
					fmt.Fprintln(os.Stderr, "thumb", id, "http", resp.StatusCode)
					resp.Body.Close()
					continue
				}
				data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
				resp.Body.Close()
				if err == nil && len(data) > 0 {
					os.WriteFile(dst, data, 0644)
				}
			}
		}()
	}
	wg.Wait()
	fmt.Printf("%d results\n", len(results))
}

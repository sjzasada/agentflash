package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"agentflash/internal/tap"
	"agentflash/internal/ui"
)

//go:embed web
var webFS embed.FS

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "__tap" {
		os.Args = append(os.Args[:1], os.Args[2:]...)
		runTap()
		return
	}
	runUI()
}

func runTap() {
	fs := flag.NewFlagSet("__tap", flag.ExitOnError)
	dir := fs.String("dir", "", "directory to watch")
	excludePIDCSV := fs.String("exclude-pid", "", "comma-separated PIDs to drop events from")
	excludeNameCSV := fs.String("exclude-name", "", "comma-separated process names to drop events from")
	_ = fs.Parse(os.Args[1:])
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "tap: --dir is required")
		os.Exit(2)
	}
	pids, err := parsePIDs(*excludePIDCSV)
	if err != nil {
		log.Fatalf("tap: --exclude-pid: %v", err)
	}
	names := parseNames(*excludeNameCSV)
	cfg := tap.Config{WatchDir: *dir, ExcludePID: pids, ExcludeName: names}
	if err := tap.Run(cfg, os.Stdout); err != nil {
		log.Fatalf("tap: %v", err)
	}
}

func parseNames(csv string) []string {
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parsePIDs(csv string) ([]int, error) {
	if csv == "" {
		return nil, nil
	}
	parts := strings.Split(csv, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid pid %q", p)
		}
		out = append(out, n)
	}
	return out, nil
}

func runUI() {
	fs := flag.NewFlagSet("agentflash", flag.ExitOnError)
	dir := fs.String("dir", "", "directory to watch (required)")
	addr := fs.String("addr", "127.0.0.1:7777", "HTTP listen address")
	ringSize := fs.Int("buffer", 10000, "ring buffer size for replayed history")
	_ = fs.Parse(os.Args[1:])
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "agentflash: --dir is required")
		os.Exit(2)
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		log.Fatalf("resolve --dir: %v", err)
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		log.Fatalf("--dir %q is not a directory", abs)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := ui.Config{
		Dir:       abs,
		Addr:      *addr,
		WebFS:     webFS,
		WebPrefix: "web",
		RingSize:  *ringSize,
	}
	if err := ui.Run(ctx, cfg); err != nil {
		log.Fatalf("ui: %v", err)
	}
}

package main

import (
	"context"
	"embed"
	"encoding/json"
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

var version = "dev" // set via -ldflags "-X main.version=..." at build time

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			printTopLevelHelp(os.Stdout)
			return
		case "--version", "-version", "version":
			fmt.Println(version)
			return
		case "__tap":
			os.Args = append(os.Args[:1], os.Args[2:]...)
			runTap()
			return
		case "hooks":
			os.Args = append(os.Args[:1], os.Args[2:]...)
			runHooks()
			return
		}
	}
	runUI()
}

func printTopLevelHelp(w *os.File) {
	fmt.Fprint(w, `agentflash — real-time visualizer of filesystem activity

Usage:
  agentflash --dir <path> [flags]    run the UI (default)
  agentflash hooks [flags]           print or merge Claude Code hooks
  agentflash __tap ...               internal; spawned by the UI under sudo

Run "agentflash <subcommand> --help" for per-subcommand flags.
`)
}

func runHooks() {
	fs := flag.NewFlagSet("hooks", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:7777", "address of the running agentflash UI to post events to")
	apply := fs.Bool("apply", false, "merge into the settings file instead of printing")
	pathFlag := fs.String("path", "~/.claude/settings.json", "settings file to print or modify")
	_ = fs.Parse(os.Args[1:])

	url := "http://" + *addr + "/api/claude/event"
	cmd := "curl -fsS -X POST " + url +
		" -H 'Content-Type: application/json' --data-binary @-"
	hooksByEvent := buildHooksMap(cmd)

	if !*apply {
		block := map[string]any{"hooks": hooksByEvent}
		out, err := json.MarshalIndent(block, "", "  ")
		if err != nil {
			log.Fatalf("hooks: marshal: %v", err)
		}
		fmt.Fprintln(os.Stderr, "# Paste this into "+*pathFlag+" (or rerun with --apply to merge automatically)."+
			" Make sure agentflash is running on "+*addr+".")
		fmt.Println(string(out))
		return
	}

	target, err := expandHome(*pathFlag)
	if err != nil {
		log.Fatalf("hooks: %v", err)
	}
	changed, err := applyHooks(target, hooksByEvent, url)
	if err != nil {
		log.Fatalf("hooks: %v", err)
	}
	if changed {
		fmt.Fprintf(os.Stderr, "hooks: wrote %s\n", target)
	} else {
		fmt.Fprintf(os.Stderr, "hooks: %s already up to date\n", target)
	}
}

func buildHooksMap(cmd string) map[string][]hookEntry {
	mk := func(matcher string) []hookEntry {
		e := hookEntry{Hooks: []hookCmd{{Type: "command", Command: cmd, Timeout: 5}}}
		if matcher != "" {
			e.Matcher = matcher
		}
		return []hookEntry{e}
	}
	return map[string][]hookEntry{
		"PreToolUse":       mk(".*"),
		"PostToolUse":      mk(".*"),
		"UserPromptSubmit": mk(""),
		"SessionStart":     mk(""),
		"Stop":             mk(""),
		"Notification":     mk(""),
		"SubagentStop":     mk(""),
	}
}

// applyHooks merges our hooks into the settings file. Returns true if
// the file was modified. Existing entries pointing at our own URL are
// replaced; other unrelated hooks under the same event are kept.
func applyHooks(path string, ours map[string][]hookEntry, url string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	var settings map[string]any
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(data) > 0 {
			if err := json.Unmarshal(data, &settings); err != nil {
				return false, fmt.Errorf("parse %s: %w (refusing to overwrite)", path, err)
			}
		}
	case os.IsNotExist(err):
		// fresh file
	default:
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if settings == nil {
		settings = map[string]any{}
	}

	hooksAny, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooksAny = map[string]any{}
	}

	for evt, entries := range ours {
		// Drop any prior entry whose hook[*].command points at our URL.
		existing, _ := hooksAny[evt].([]any)
		kept := existing[:0:0]
		for _, e := range existing {
			if entryReferencesURL(e, url) {
				continue
			}
			kept = append(kept, e)
		}
		// Append our entries.
		for _, our := range entries {
			b, _ := json.Marshal(our)
			var asAny any
			_ = json.Unmarshal(b, &asAny)
			kept = append(kept, asAny)
		}
		hooksAny[evt] = kept
	}
	settings["hooks"] = hooksAny

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal: %w", err)
	}
	out = append(out, '\n')
	// No-op if identical to existing content.
	if len(data) > 0 && string(data) == string(out) {
		return false, nil
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

func entryReferencesURL(entry any, url string) bool {
	em, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := em["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hooks {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := hm["command"].(string)
		if strings.Contains(cmd, url) {
			return true
		}
	}
	return false
}

func expandHome(p string) (string, error) {
	if !strings.HasPrefix(p, "~") {
		return filepath.Abs(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}

type hookEntry struct {
	Matcher string    `json:"matcher,omitempty"`
	Hooks   []hookCmd `json:"hooks"`
}

type hookCmd struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

func runTap() {
	fs := flag.NewFlagSet("__tap", flag.ExitOnError)
	dir := fs.String("dir", "", "directory to watch (required)")
	excludePIDCSV := fs.String("exclude-pid", "", "comma-separated PIDs to drop events from")
	excludeNameCSV := fs.String("exclude-name", "", "comma-separated process names to drop events from")
	rawDump := fs.String("raw-dump", "", "append every raw kernel-tap line to this file (debug)")
	debug := fs.Bool("debug", false, "verbose diagnostics")
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
	cfg := tap.Config{
		WatchDir:    *dir,
		ExcludePID:  pids,
		ExcludeName: names,
		RawDumpFile: *rawDump,
		Debug:       *debug,
	}
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
	rawDump := fs.String("raw-dump", "", "append every raw kernel-tap line to this file (debug)")
	debug := fs.Bool("debug", false, "verbose diagnostics")
	autoPause := fs.Bool("auto-pause", false, "pause the timeline when Claude's Stop hook fires (resumes on next prompt)")
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
		Dir:         abs,
		Addr:        *addr,
		WebFS:       webFS,
		WebPrefix:   "web",
		RingSize:    *ringSize,
		RawDumpFile: *rawDump,
		Debug:       *debug,
		AutoPause:   *autoPause,
	}
	if err := ui.Run(ctx, cfg); err != nil {
		log.Fatalf("ui: %v", err)
	}
}

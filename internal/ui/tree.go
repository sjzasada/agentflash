package ui

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type treeEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
}

func infoHandler(root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"root": root})
	}
}

// treeHandler returns one directory level of the watched dir. Query
// param `path` is a relative path under root; empty means the root.
func treeHandler(root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rel := r.URL.Query().Get("path")
		// Normalize: forbid absolute paths and `..` escapes.
		if strings.HasPrefix(rel, "/") {
			http.Error(w, "absolute path not allowed", http.StatusBadRequest)
			return
		}
		full := filepath.Join(root, rel)
		clean, err := filepath.Abs(full)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rootAbs, _ := filepath.Abs(root)
		if clean != rootAbs && !strings.HasPrefix(clean, rootAbs+string(filepath.Separator)) {
			http.Error(w, "path escapes root", http.StatusBadRequest)
			return
		}
		entries, err := os.ReadDir(clean)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]treeEntry, 0, len(entries))
		for _, e := range entries {
			out = append(out, treeEntry{Name: e.Name(), IsDir: e.IsDir()})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].IsDir != out[j].IsDir {
				return out[i].IsDir
			}
			return out[i].Name < out[j].Name
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

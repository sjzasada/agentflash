package ui

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestTreeHandler(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := treeHandler(root)

	t.Run("root", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/tree?path=", nil)
		rr := httptest.NewRecorder()
		h(rr, req)
		if rr.Code != 200 {
			t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
		}
		var got []treeEntry
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("want 2 entries, got %d", len(got))
		}
		if !got[0].IsDir || got[0].Name != "sub" {
			t.Errorf("first entry should be dir 'sub', got %+v", got[0])
		}
		if got[1].IsDir || got[1].Name != "a.txt" {
			t.Errorf("second entry should be file 'a.txt', got %+v", got[1])
		}
	})

	t.Run("subdir", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/tree?path=sub", nil)
		rr := httptest.NewRecorder()
		h(rr, req)
		if rr.Code != 200 {
			t.Fatalf("status %d", rr.Code)
		}
		var got []treeEntry
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Name != "b.txt" {
			t.Errorf("unexpected: %+v", got)
		}
	})

	t.Run("escape rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/tree?path=../../etc", nil)
		rr := httptest.NewRecorder()
		h(rr, req)
		if rr.Code != 400 {
			t.Errorf("want 400, got %d", rr.Code)
		}
	})

	t.Run("absolute rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/tree?path=/etc", nil)
		rr := httptest.NewRecorder()
		h(rr, req)
		if rr.Code != 400 {
			t.Errorf("want 400, got %d", rr.Code)
		}
	})
}

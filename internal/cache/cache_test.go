package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadSnapshot(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SaveSnapshot("42", "<p>remote</p>", "# local", 7); err != nil {
		t.Fatalf("save: %v", err)
	}
	rem, loc, meta, err := c.LoadSnapshot("42")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if rem != "<p>remote</p>" || loc != "# local" {
		t.Errorf("bodies: rem=%q loc=%q", rem, loc)
	}
	if meta.Version != 7 {
		t.Errorf("version = %d", meta.Version)
	}
	if meta.SyncedAt.IsZero() {
		t.Errorf("synced_at not set")
	}
}

func TestLoadSnapshot_Missing(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Should not error on missing pages.
	rem, loc, meta, err := c.LoadSnapshot("nope")
	if err != nil {
		t.Errorf("missing page: %v", err)
	}
	if rem != "" || loc != "" || meta.Version != 0 {
		t.Errorf("expected zero values")
	}
}

func TestWriteDiffPair(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	left, right, err := c.WriteDiffPair("42", "local\n", "remote\n")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(left) != "diff.local.md" {
		t.Errorf("left filename: %s", left)
	}
	if filepath.Base(right) != "diff.remote.md" {
		t.Errorf("right filename: %s", right)
	}
	lb, _ := os.ReadFile(left)
	rb, _ := os.ReadFile(right)
	if string(lb) != "local\n" || string(rb) != "remote\n" {
		t.Errorf("content mismatch")
	}
}

func TestNew_EmptyRoot(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Errorf("expected error on empty root")
	}
}

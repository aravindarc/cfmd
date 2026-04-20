// Package cache persists per-page snapshots used by conflict detection and
// the diff UX.
//
// Layout:
//
//	<root>/pages/<page_id>/last_remote.xhtml   storage format at last sync
//	<root>/pages/<page_id>/last_local.md       markdown body at last sync
//	<root>/pages/<page_id>/meta.json           {version, synced_at}
//	<root>/pages/<page_id>/diff.local.md       most recent diff left side
//	<root>/pages/<page_id>/diff.remote.md      most recent diff right side
package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Cache is a handle to the on-disk cache directory.
type Cache struct {
	root string
}

// New returns a Cache rooted at the given directory. It creates the root if
// missing.
func New(root string) (*Cache, error) {
	if root == "" {
		return nil, fmt.Errorf("cache root is empty")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir cache root: %w", err)
	}
	return &Cache{root: root}, nil
}

// Meta is persisted per page for conflict-detection heuristics.
type Meta struct {
	Version  int       `json:"version"`
	SyncedAt time.Time `json:"synced_at"`
}

// pageDir returns the directory for a page id, creating it if needed.
func (c *Cache) pageDir(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("empty page id")
	}
	dir := filepath.Join(c.root, "pages", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// SaveSnapshot writes the remote storage XML, local markdown body, and meta.
// Any of the three can be empty to skip writing it.
func (c *Cache) SaveSnapshot(id, remoteXHTML, localBody string, version int) error {
	dir, err := c.pageDir(id)
	if err != nil {
		return err
	}
	if remoteXHTML != "" {
		if err := os.WriteFile(filepath.Join(dir, "last_remote.xhtml"), []byte(remoteXHTML), 0o600); err != nil {
			return err
		}
	}
	if localBody != "" {
		if err := os.WriteFile(filepath.Join(dir, "last_local.md"), []byte(localBody), 0o600); err != nil {
			return err
		}
	}
	m := Meta{Version: version, SyncedAt: time.Now().UTC()}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), b, 0o600)
}

// LoadSnapshot returns the last-saved snapshot for a page. Missing files
// return empty strings without error (first-sync is not an error).
func (c *Cache) LoadSnapshot(id string) (remoteXHTML, localBody string, meta Meta, err error) {
	dir := filepath.Join(c.root, "pages", id)
	if b, e := os.ReadFile(filepath.Join(dir, "last_remote.xhtml")); e == nil {
		remoteXHTML = string(b)
	}
	if b, e := os.ReadFile(filepath.Join(dir, "last_local.md")); e == nil {
		localBody = string(b)
	}
	if b, e := os.ReadFile(filepath.Join(dir, "meta.json")); e == nil {
		_ = json.Unmarshal(b, &meta)
	}
	return
}

// WriteDiffPair writes the left and right sides of a diff preview into the
// per-page cache. Returns the two file paths — commonly used to print an
// `idea diff A B` command line.
func (c *Cache) WriteDiffPair(id, local, remote string) (leftPath, rightPath string, err error) {
	dir, err := c.pageDir(id)
	if err != nil {
		return "", "", err
	}
	leftPath = filepath.Join(dir, "diff.local.md")
	rightPath = filepath.Join(dir, "diff.remote.md")
	if err := os.WriteFile(leftPath, []byte(local), 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(rightPath, []byte(remote), 0o600); err != nil {
		return "", "", err
	}
	return leftPath, rightPath, nil
}

// Root returns the cache root directory.
func (c *Cache) Root() string { return c.root }

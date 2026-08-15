package firebase

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client is a minimal REST client for Firebase Realtime Database, mirroring
// the raw GET/PUT/DELETE semantics of the old TypeScript codebase so the data
// layout stays byte-for-byte compatible.
type Client struct {
	base string
	http *http.Client
}

func New(base string, hc *http.Client) *Client {
	return &Client{base: strings.TrimSuffix(base, "/"), http: hc}
}

func pathJoin(base, p string) string {
	return base + "/" + p + ".json"
}

// Get returns the raw JSON at the path or nil when the node is absent, the
// request fails, or the payload is not valid JSON (mirrors the TS helper that
// swallowed errors).
func (c *Client) Get(ctx context.Context, path string) json.RawMessage {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pathJoin(c.base, path), nil)
	if err != nil {
		return nil
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	// return raw bytes untouched so round-trips are exact and valid JSON
	return body
}

func (c *Client) Put(ctx context.Context, path string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, pathJoin(c.base, path), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("firebase PUT %s: HTTP %d", path, resp.StatusCode)
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, pathJoin(c.base, path), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("firebase DELETE %s: HTTP %d", path, resp.StatusCode)
	}
	return nil
}

// Patch sends an HTTP PATCH, which merges the given object into the node at
// path without touching its other keys. Unlike PUT (which replaces the whole
// node and deletes any children), PATCH is required for nodes that live inside
// a shared parent — the per-directory index nodes under fs/{id}/index would
// otherwise be wiped every time the root index is written (see vfs.writeIndex).
func (c *Client) Patch(ctx context.Context, path string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, pathJoin(c.base, path), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("firebase PATCH %s: HTTP %d", path, resp.StatusCode)
	}
	return nil
}

// ListKeys returns the direct child keys of a node via a shallow GET
// (?shallow=true). Returns nil when the node is absent or the request fails.
// Used by VFS.DeleteDir to enumerate the content/index stores independently of
// the directory index, which Firebase RTDB eventual consistency can leave
// stale after read-modify-write races.
func (c *Client) ListKeys(ctx context.Context, path string) []string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pathJoin(c.base, path)+"?shallow=true", nil)
	if err != nil {
		return nil
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	var keys map[string]bool
	if err := json.Unmarshal(body, &keys); err != nil {
		return nil
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	return out
}

// NormalizePath normalizes a user-supplied path exactly like the TS code:
// backslashes to slashes, collapsing repeated slashes, stripping leading and
// trailing slashes.
func NormalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	p = strings.Trim(p, "/")
	return p
}

// Base64BID encodes a string the same way Buffer.toString("base64url") does
// (standard base64url alphabet with padding retained).
func Base64(s string) string {
	return base64.URLEncoding.EncodeToString([]byte(s))
}

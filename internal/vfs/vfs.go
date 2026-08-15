// Package vfs implements the per-user virtual file system stored in Firebase
// RTDB. The index layout mirrors the TS implementation exactly so existing
// user data keeps working: fs/{chat}/index[/{b64path}] holds {"entries":[...]}
// and fs/{chat}/content/{b64path} holds the file body.
package vfs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
)

type VFS struct {
	fb *firebase.Client
}

type Entry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func New(fb *firebase.Client) *VFS {
	return &VFS{fb: fb}
}

func indexPath(chatID int64, p string) string {
	if p == "" {
		return "fs/" + idKey(chatID) + "/index"
	}
	return "fs/" + idKey(chatID) + "/index/" + firebase.Base64(p)
}

func contentPath(chatID int64, p string) string {
	return "fs/" + idKey(chatID) + "/content/" + firebase.Base64(p)
}

func idKey(chatID int64) string {
	return itoa(chatID)
}

type indexDoc struct {
	Entries []Entry `json:"entries"`
}

func readIndex(ctx context.Context, fb *firebase.Client, p string) *indexDoc {
	raw := fb.Get(ctx, p)
	if len(raw) == 0 {
		return &indexDoc{}
	}
	var doc indexDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return &indexDoc{}
	}
	return &doc
}

func writeIndex(ctx context.Context, fb *firebase.Client, p string, doc *indexDoc) error {
	// PATCH (not PUT): in real RTDB a PUT on fs/{id}/index replaces the whole
	// node and silently deletes the per-directory index children below it
	// (fs/{id}/index/{b64}), leaving every folder empty. PATCH merges the
	// "entries" key and keeps those child nodes intact.
	return fb.Patch(ctx, p, doc)
}

func ensureAncestors(ctx context.Context, fb *firebase.Client, chatID int64, p string) error {
	parts := splitPath(p)
	if len(parts) <= 1 {
		return nil
	}
	accumulated := ""
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		ip := indexPath(chatID, accumulated)
		doc := readIndex(ctx, fb, ip)
		found := false
		for _, e := range doc.Entries {
			if e.Name == part {
				found = true
				break
			}
		}
		if !found {
			doc.Entries = append(doc.Entries, Entry{Name: part, Type: "dir"})
			if err := writeIndex(ctx, fb, ip, doc); err != nil {
				return err
			}
		}
		if accumulated == "" {
			accumulated = part
		} else {
			accumulated = accumulated + "/" + part
		}
	}
	return nil
}

func (v *VFS) ReadFile(ctx context.Context, chatID int64, path string) (string, bool) {
	p := firebase.NormalizePath(path)
	if p == "" {
		return "", false
	}
	raw := v.fb.Get(ctx, contentPath(chatID, p))
	if len(raw) == 0 {
		return "", false
	}
	// RTDB returns literal JSON null for a missing node; treat it as absent
	// (json.Unmarshal("null", &string) would otherwise silently yield "").
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

func (v *VFS) WriteFile(ctx context.Context, chatID int64, path, content string) error {
	p := firebase.NormalizePath(path)
	if p == "" {
		return nil
	}
	if err := v.fb.Put(ctx, contentPath(chatID, p), content); err != nil {
		return err
	}
	parent, hasParent := dirname(p)
	parentPath := parent
	if !hasParent {
		parentPath = ""
	}
	ip := indexPath(chatID, parentPath)
	doc := readIndex(ctx, v.fb, ip)
	found := false
	for _, e := range doc.Entries {
		if e.Name == basename(p) {
			found = true
			break
		}
	}
	if !found {
		doc.Entries = append(doc.Entries, Entry{Name: basename(p), Type: "file"})
		if err := writeIndex(ctx, v.fb, ip, doc); err != nil {
			return err
		}
	}
	if hasParent {
		return ensureAncestors(ctx, v.fb, chatID, p)
	}
	return nil
}

func (v *VFS) DeleteFile(ctx context.Context, chatID int64, path string) (bool, error) {
	p := firebase.NormalizePath(path)
	if p == "" {
		return false, nil
	}
	cp := contentPath(chatID, p)
	raw := v.fb.Get(ctx, cp)
	if len(raw) == 0 {
		return false, nil
	}
	// RTDB returns literal JSON null for a missing node; treat it as absent so a
	// delete of a non-existent file reports "not found" instead of a phantom
	// success (mirrors the guard in ReadFile).
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, nil
	}
	if err := v.fb.Delete(ctx, cp); err != nil {
		return false, err
	}
	parent, hasParent := dirname(p)
	parentPath := parent
	if !hasParent {
		parentPath = ""
	}
	ip := indexPath(chatID, parentPath)
	doc := readIndex(ctx, v.fb, ip)
	filtered := doc.Entries[:0]
	for _, e := range doc.Entries {
		if e.Name != basename(p) {
			filtered = append(filtered, e)
		}
	}
	doc.Entries = filtered
	if err := writeIndex(ctx, v.fb, ip, doc); err != nil {
		return false, err
	}
	return true, nil
}

func (v *VFS) ListDirectory(ctx context.Context, chatID int64, path string) []Entry {
	p := firebase.NormalizePath(path)
	doc := readIndex(ctx, v.fb, indexPath(chatID, p))
	return doc.Entries
}

func (v *VFS) DeleteAll(ctx context.Context, chatID int64) error {
	return v.fb.Delete(ctx, "fs/"+idKey(chatID))
}

// DeleteDir recursively removes the directory at path. Instead of walking the
// directory index it scans the per-user content and index stores directly and
// deletes every file body and index node under the path, then drops the
// directory's entry from its parent index. Scanning the stores (not the index)
// makes the delete complete even when Firebase RTDB eventual consistency left
// an index entry missing (e.g. a skill whose SKILL.md entry was lost during
// install) — such orphaned content would otherwise survive the delete.
// Returns (false, nil) when nothing exists under the path.
func (v *VFS) DeleteDir(ctx context.Context, chatID int64, path string) (bool, error) {
	p := firebase.NormalizePath(path)
	if p == "" {
		return false, nil
	}
	id := idKey(chatID)
	deleted := false

	// File bodies live under fs/{id}/content/{b64path}; directories have no
	// content node, so only exact-path and dir+"/" prefixes match files.
	for _, key := range v.fb.ListKeys(ctx, "fs/"+id+"/content") {
		fp, ok := decodePath(key)
		if !ok || !underPath(fp, p) {
			continue
		}
		if err := v.fb.Delete(ctx, contentPath(chatID, fp)); err != nil {
			return false, err
		}
		deleted = true
	}

	// Index nodes live under fs/{id}/index/{b64path} for both files and dirs;
	// remove every node in the subtree so no empty directory residue remains.
	for _, key := range v.fb.ListKeys(ctx, "fs/"+id+"/index") {
		ip, ok := decodePath(key)
		if !ok || !underPath(ip, p) {
			continue
		}
		if err := v.fb.Delete(ctx, indexPath(chatID, ip)); err != nil {
			return false, err
		}
		deleted = true
	}

	if !deleted {
		return false, nil
	}

	parent, hasParent := dirname(p)
	parentPath := parent
	if !hasParent {
		parentPath = ""
	}
	doc := readIndex(ctx, v.fb, indexPath(chatID, parentPath))
	filtered := doc.Entries[:0]
	for _, e := range doc.Entries {
		if e.Name != basename(p) {
			filtered = append(filtered, e)
		}
	}
	if len(filtered) != len(doc.Entries) {
		doc.Entries = filtered
		if err := writeIndex(ctx, v.fb, indexPath(chatID, parentPath), doc); err != nil {
			return false, err
		}
	}
	return true, nil
}

// decodePath reverses firebase.Base64 (URL-safe base64 with padding).
func decodePath(enc string) (string, bool) {
	raw, err := base64.URLEncoding.DecodeString(enc)
	if err != nil {
		return "", false
	}
	return firebase.NormalizePath(string(raw)), true
}

// underPath reports whether p is dir itself or nested under dir.
func underPath(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+"/")
}

func (v *VFS) MoveFile(ctx context.Context, chatID int64, src, dst string) (success bool, errMsg string) {
	s := firebase.NormalizePath(src)
	d := firebase.NormalizePath(dst)
	if s == "" || d == "" {
		return false, "Invalid path"
	}
	content, ok := v.ReadFile(ctx, chatID, s)
	if !ok {
		return false, "Source file not found"
	}
	if _, ok := v.ReadFile(ctx, chatID, d); ok {
		return false, "Destination already exists"
	}
	if err := v.WriteFile(ctx, chatID, d, content); err != nil {
		return false, "Failed to write destination"
	}
	deleted, err := v.DeleteFile(ctx, chatID, s)
	if err != nil || !deleted {
		// roll back the copied file
		v.DeleteFile(ctx, chatID, d)
		return false, "Failed to remove source after copy"
	}
	return true, ""
}

func (v *VFS) EditFile(ctx context.Context, chatID int64, path, oldString, newString string) (success bool, errMsg string) {
	content, ok := v.ReadFile(ctx, chatID, path)
	if !ok {
		return false, "File not found"
	}
	if !contains(content, oldString) {
		return false, "old_string not found in file"
	}
	newContent := replaceFirst(content, oldString, newString)
	if err := v.WriteFile(ctx, chatID, path, newContent); err != nil {
		return false, "Failed to write file"
	}
	return true, ""
}

func contains(s, sub string) bool {
	return len(sub) == 0 || indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func replaceFirst(s, old, nw string) string {
	i := indexOf(s, old)
	if i < 0 || old == "" {
		return s
	}
	return s[:i] + nw + s[i+len(old):]
}

func dirname(p string) (string, bool) {
	idx := lastIndexOf(p, '/')
	if idx < 0 {
		return "", false
	}
	return p[:idx], true
}

func basename(p string) string {
	idx := lastIndexOf(p, '/')
	if idx < 0 {
		return p
	}
	return p[idx+1:]
}

func lastIndexOf(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func splitPath(p string) []string {
	parts := []string{}
	cur := ""
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			if cur != "" {
				parts = append(parts, cur)
				cur = ""
			}
		} else {
			cur += string(p[i])
		}
	}
	if cur != "" {
		parts = append(parts, cur)
	}
	return parts
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

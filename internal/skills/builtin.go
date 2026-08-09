package skills

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// Builtin SKILL.md files bundled from picoclaw (MIT, see
// https://github.com/sipeed/picoclaw) — weather, summarize, github, skill-creator.
//
//go:embed all:builtin
var builtinFS embed.FS

// ListBuiltinSkills returns the names of bundled built-in skills.
func ListBuiltinSkills() []string {
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// InstallBuiltin copies a bundled skill into the per-user VFS.
func (r *Registry) InstallBuiltin(ctx context.Context, chatID int64, name string) InstallResult {
	if r.vfs == nil {
		return InstallResult{Success: false, Error: "VFS tidak tersedia"}
	}
	if msg := ValidateSkillName(name); msg != "" {
		return InstallResult{Success: false, Error: msg}
	}
	if _, err := fs.Stat(builtinFS, "builtin/"+name+"/SKILL.md"); err != nil {
		return InstallResult{Success: false, Error: fmt.Sprintf("Skill bawaan %q tidak ditemukan", name)}
	}
	if _, ok := r.vfs.ReadFile(ctx, chatID, "skills/"+name+"/SKILL.md"); ok {
		return InstallResult{Success: false, Error: fmt.Sprintf("Skill %q sudah terinstall", name)}
	}
	var firstErr error
	prefix := "builtin/" + name + "/"
	err := fs.WalkDir(builtinFS, "builtin/"+name, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := builtinFS.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel := strings.TrimPrefix(path, prefix)
		if werr := r.vfs.WriteFile(ctx, chatID, "skills/"+name+"/"+rel, string(data)); werr != nil && firstErr == nil {
			firstErr = werr
		}
		return nil
	})
	if err != nil {
		return InstallResult{Success: false, Error: "Gagal install skill bawaan: " + err.Error()}
	}
	if firstErr != nil {
		return InstallResult{Success: false, Error: "Gagal simpan skill ke penyimpanan: " + firstErr.Error()}
	}
	return InstallResult{Success: true, Name: name, Path: "skills/" + name + "/SKILL.md"}
}

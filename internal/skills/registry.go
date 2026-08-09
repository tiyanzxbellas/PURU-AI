package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
	pcconfig "github.com/sipeed/picoclaw/pkg/config"
	pcskills "github.com/sipeed/picoclaw/pkg/skills"
)

const (
	maxSearchResult       = 20
	maxSkillFileSize      = 2 << 20
	defaultGitHubBaseURL  = "https://github.com"
	defaultClawHubBaseURL = "https://clawhub.ai"
)

type SearchResult struct {
	Score        float64
	Slug         string
	DisplayName  string
	Summary      string
	Version      string
	RegistryName string
	URL          string
}

type InstallResult struct {
	Success      bool
	Name         string
	Path         string
	Error        string
	IsSuspicious bool
	Warning      string
}

// RegistryOptions wires the skill registry manager. The picoclaw registry
// logic is imported (github code search filename:SKILL.md, clawhub, cache)
// and its file-based installer is adapted to write into the per-user VFS.
type RegistryOptions struct {
	GitHubToken           string // optional; required for GitHub code search
	ClawHubToken          string // optional ClawHub token; enables the clawhub registry
	MaxConcurrentSearches int    // default 2
}

type Registry struct {
	vfs            *vfs.VFS
	manager        *pcskills.RegistryManager
	githubToken    string
	clawhubEnabled bool
}

func NewRegistry(v *vfs.VFS, opts RegistryOptions) *Registry {
	maxConc := opts.MaxConcurrentSearches
	if maxConc <= 0 {
		maxConc = 2
	}
	clawhubEnabled := opts.ClawHubToken != ""

	cfg := pcconfig.SkillsToolsConfig{
		Registries: []*pcconfig.SkillRegistryConfig{
			{
				Name:      "github",
				Enabled:   true,
				BaseURL:   defaultGitHubBaseURL,
				AuthToken: *pcconfig.NewSecureString(opts.GitHubToken),
			},
		},
		MaxConcurrentSearches: maxConc,
	}
	if clawhubEnabled {
		cfg.Registries = append(cfg.Registries, &pcconfig.SkillRegistryConfig{
			Name:      "clawhub",
			Enabled:   true,
			BaseURL:   defaultClawHubBaseURL,
			AuthToken: *pcconfig.NewSecureString(opts.ClawHubToken),
		})
	}

	return &Registry{
		vfs:            v,
		manager:        pcskills.NewRegistryManagerFromToolsConfig(cfg),
		githubToken:    opts.GitHubToken,
		clawhubEnabled: clawhubEnabled,
	}
}

// SearchSkills searches every enabled registry (GitHub, ClawHub) concurrently
// and merges results sorted by score. Requires GITHUB_TOKEN — GitHub's code
// search API returns HTTP 401 "Requires authentication" without one.
func (r *Registry) SearchSkills(ctx context.Context, query string) ([]SearchResult, error) {
	if r.manager == nil {
		return nil, fmt.Errorf("registry tidak dikonfigurasi")
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query pencarian kosong")
	}
	if r.githubToken == "" && !r.clawhubEnabled {
		return nil, fmt.Errorf("GITHUB_TOKEN belum diset di .env — GitHub code search membutuhkan token (lihat /skills)")
	}

	results, err := r.manager.SearchAll(ctx, strings.TrimSpace(query), maxSearchResult)
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(results))
	for _, res := range results {
		url := ""
		if reg := r.manager.GetRegistry(res.RegistryName); reg != nil {
			url = reg.SkillURL(res.Slug, res.Version)
		}
		out = append(out, SearchResult{
			Score:        res.Score,
			Slug:         res.Slug,
			DisplayName:  res.DisplayName,
			Summary:      res.Summary,
			Version:      res.Version,
			RegistryName: res.RegistryName,
			URL:          url,
		})
	}
	return out, nil
}

// InstallFromGitHub installs a GitHub skill (slug owner/repo[/subpath] or URL)
// into the per-user VFS. picoclaw downloads to a temp dir on disk first; the
// files are then moved into the VFS and the disk copy is removed.
func (r *Registry) InstallFromGitHub(ctx context.Context, chatID int64, target string) InstallResult {
	return r.installFromRegistry(ctx, chatID, "github", target)
}

// InstallFromClawHub installs a skill from ClawHub by slug.
func (r *Registry) InstallFromClawHub(ctx context.Context, chatID int64, slugTarget string) InstallResult {
	return r.installFromRegistry(ctx, chatID, "clawhub", slugTarget)
}

func (r *Registry) installFromRegistry(ctx context.Context, chatID int64, registryName, target string) InstallResult {
	if r.manager == nil {
		return InstallResult{Error: "registry manager tidak dikonfigurasi"}
	}
	if r.vfs == nil {
		return InstallResult{Error: "VFS tidak tersedia"}
	}
	reg := r.manager.GetRegistry(registryName)
	if reg == nil {
		return InstallResult{Error: fmt.Sprintf("Registry %q tidak aktif", registryName)}
	}

	dirName, err := reg.ResolveInstallDirName(target)
	if err != nil || dirName == "" {
		return InstallResult{Error: "Target install tidak valid"}
	}
	if _, ok := r.vfs.ReadFile(ctx, chatID, "skills/"+dirName+"/SKILL.md"); ok {
		return InstallResult{Error: fmt.Sprintf("Skill %q sudah terinstall", dirName)}
	}

	tmpDir, err := os.MkdirTemp("", "puru-skill-")
	if err != nil {
		return InstallResult{Error: "Gagal membuat direktori sementara: " + err.Error()}
	}
	defer os.RemoveAll(tmpDir)

	res, err := reg.DownloadAndInstall(ctx, target, "", tmpDir)
	if err != nil {
		return InstallResult{Error: "Gagal install skill: " + err.Error()}
	}
	if res.IsMalwareBlocked {
		return InstallResult{Error: "Skill tidak dapat diinstall (terdeteksi sebagai malware)"}
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "SKILL.md")); err != nil {
		return InstallResult{Error: "SKILL.md tidak ditemukan di repository"}
	}

	if err := r.moveDiskToVFS(ctx, tmpDir, chatID, dirName, maxSkillFileSize); err != nil {
		return InstallResult{Error: "Gagal menyimpan skill ke penyimpanan: " + err.Error()}
	}
	r.writeOriginMeta(ctx, chatID, dirName, registryName, target, res.Version)

	result := InstallResult{Success: true, Name: dirName, Path: "skills/" + dirName + "/SKILL.md"}
	if res.IsSuspicious {
		result.IsSuspicious = true
		result.Warning = "Skill ditandai mencurigakan — gunakan dengan hati-hati."
	}
	if res.Summary != "" {
		result.Warning = strings.TrimSpace(res.Summary)
	}
	return result
}

// moveDiskToVFS walks the temp install dir and writes every file into
// skills/<dirName>/... in the per-user VFS (files > maxBytes are skipped).
func (r *Registry) moveDiskToVFS(ctx context.Context, tmpDir string, chatID int64, dirName string, maxBytes int64) error {
	return filepath.Walk(tmpDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(tmpDir, p)
		if err != nil {
			return err
		}
		if info.Size() > maxBytes {
			return fmt.Errorf("file %s melebihi batas ukuran", rel)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return r.vfs.WriteFile(ctx, chatID, "skills/"+dirName+"/"+rel, string(data))
	})
}

type installedOriginMeta struct {
	Version          int    `json:"version"`
	OriginKind       string `json:"origin_kind,omitempty"`
	Registry         string `json:"registry,omitempty"`
	Slug             string `json:"slug,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	InstalledAt      int64  `json:"installed_at"`
}

func (r *Registry) writeOriginMeta(ctx context.Context, chatID int64, dirName, registry, slug, version string) {
	meta := installedOriginMeta{
		Version:          1,
		OriginKind:       "third_party",
		Registry:         registry,
		Slug:             slug,
		InstalledVersion: version,
		InstalledAt:      time.Now().UnixMilli(),
	}
	if b, err := json.MarshalIndent(meta, "", "  "); err == nil {
		_ = r.vfs.WriteFile(ctx, chatID, "skills/"+dirName+"/.skill-origin.json", string(b))
	}
}

// InstallFromContent stores an inline skill (created by the agent).
func (r *Registry) InstallFromContent(ctx context.Context, chatID int64, name, description, body string) InstallResult {
	if msg := ValidateSkillName(name); msg != "" {
		return InstallResult{Success: false, Error: msg}
	}
	if _, ok := r.vfs.ReadFile(ctx, chatID, "skills/"+name+"/SKILL.md"); ok {
		return InstallResult{Success: false, Error: fmt.Sprintf("Skill %q sudah terinstall", name)}
	}
	content := BuildSkillContent(name, description, body, "", nil)
	skillPath := "skills/" + name + "/SKILL.md"
	if err := r.vfs.WriteFile(ctx, chatID, skillPath, content); err != nil {
		return InstallResult{Success: false, Error: err.Error()}
	}
	return InstallResult{Success: true, Name: name, Path: skillPath}
}

// UpdateSkill overwrites an existing inline skill body.
func (r *Registry) UpdateSkill(ctx context.Context, chatID int64, name, description, body string) InstallResult {
	if msg := ValidateSkillName(name); msg != "" {
		return InstallResult{Success: false, Error: msg}
	}
	if _, ok := r.vfs.ReadFile(ctx, chatID, "skills/"+name+"/SKILL.md"); !ok {
		return InstallResult{Success: false, Error: fmt.Sprintf("Skill %q tidak ditemukan", name)}
	}
	content := BuildSkillContent(name, description, body, "", nil)
	skillPath := "skills/" + name + "/SKILL.md"
	if err := r.vfs.WriteFile(ctx, chatID, skillPath, content); err != nil {
		return InstallResult{Success: false, Error: err.Error()}
	}
	return InstallResult{Success: true, Name: name, Path: skillPath}
}

func (r *Registry) MigrateOldSkills(ctx context.Context, chatID int64) (migrated int, errors []string) {
	cat := NewCatalog(r.vfs)
	entries := r.vfs.ListDirectory(ctx, chatID, "skills")
	for _, entry := range entries {
		if entry.Name == "" || entry.Type != "file" || !strings.HasSuffix(entry.Name, ".md") {
			continue
		}
		oldName := strings.TrimSuffix(entry.Name, ".md")
		oldPath := "skills/" + entry.Name
		newPath := "skills/" + oldName + "/SKILL.md"
		content, ok := r.vfs.ReadFile(ctx, chatID, oldPath)
		if !ok {
			continue
		}
		if _, ok := r.vfs.ReadFile(ctx, chatID, newPath); ok {
			errors = append(errors, fmt.Sprintf("Skill %q sudah ada di format baru", oldName))
			continue
		}
		metadata := cat.ParseFrontmatter(content)
		skillName := metadata.Name
		if skillName == "" {
			skillName = oldName
		}
		description := metadata.Description
		if description == "" {
			description = "Skill: " + skillName
		}
		_, body := SplitFrontmatter(content)
		newContent := BuildSkillContent(skillName, description, body, "", nil)
		if err := r.vfs.WriteFile(ctx, chatID, newPath, newContent); err != nil {
			errors = append(errors, err.Error())
			continue
		}
		if _, err := r.vfs.DeleteFile(ctx, chatID, oldPath); err != nil {
			errors = append(errors, err.Error())
			continue
		}
		migrated++
	}
	return migrated, errors
}

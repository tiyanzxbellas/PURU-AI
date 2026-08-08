package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/purujawa/puru-ai/internal/vfs"
)

type SearchResult struct {
	Slug        string
	DisplayName string
	Summary     string
	URL         string
}

type InstallResult struct {
	Success bool
	Name    string
	Error   string
	Path    string
}

type GitHubRef struct {
	Owner       string
	Repo        string
	Ref         string
	SubPath     string
	ExplicitRef bool
}

const (
	githubAPIBase   = "https://api.github.com"
	rawGitHubBase   = "https://raw.githubusercontent.com"
	skillMarkdown   = "SKILL.md"
	maxSearchResult = 20
	maxRetries      = 3
)

type Registry struct {
	VFS  *vfs.VFS
	HTTP *http.Client
}

func NewRegistry(v *vfs.VFS, hc *http.Client) *Registry {
	if hc == nil {
		hc = &http.Client{}
	}
	return &Registry{VFS: v, HTTP: hc}
}

func parseGitHubRef(input string) *GitHubRef {
	trimmed := strings.TrimSpace(input)
	if strings.HasPrefix(trimmed, "https://github.com/") || strings.HasPrefix(trimmed, "http://github.com/") {
		u, err := url.Parse(trimmed)
		if err != nil {
			return nil
		}
		parts := splitNonEmpty(u.Path, "/")
		if len(parts) < 2 {
			return nil
		}
		ref := &GitHubRef{Owner: parts[0], Repo: parts[1], Ref: "main", ExplicitRef: false}
		treeIdx := -1
		for i, p := range parts {
			if p == "tree" {
				treeIdx = i
				break
			}
		}
		if treeIdx != -1 && treeIdx+1 < len(parts) {
			ref.Ref = parts[treeIdx+1]
			ref.ExplicitRef = true
			if treeIdx+2 < len(parts) {
				ref.SubPath = strings.Join(parts[treeIdx+2:], "/")
			}
		}
		return ref
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		return nil
	}
	ref := &GitHubRef{Owner: parts[0], Repo: parts[1], Ref: "main", ExplicitRef: false}
	if len(parts) > 2 {
		ref.SubPath = strings.Join(parts[2:], "/")
	}
	return ref
}

func splitNonEmpty(s, sep string) []string {
	var out []string
	for _, p := range strings.Split(s, sep) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (r *Registry) fetchWithRetry(ctx context.Context, url string) ([]byte, int, error) {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := r.HTTP.Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, rerr := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
			resp.Body.Close()
			if rerr != nil {
				return nil, resp.StatusCode, rerr
			}
			return body, resp.StatusCode, nil
		}
		if attempt < maxRetries {
			sleepMS(ctx, 1000*attempt)
		}
	}
	return nil, 0, lastErr
}

func (r *Registry) fetchGitHubTree(ctx context.Context, owner, repo, ref string) ([]string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1", githubAPIBase, owner, repo, ref)
	body, status, err := r.fetchWithRetry(ctx, url)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("GitHub API error: %d", status)
	}
	var data struct {
		Tree []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	var paths []string
	for _, item := range data.Tree {
		if item.Type == "blob" {
			paths = append(paths, item.Path)
		}
	}
	return paths, nil
}

func (r *Registry) fetchRawFile(ctx context.Context, owner, repo, ref, filePath string) (string, error) {
	url := fmt.Sprintf("%s/%s/%s/%s/%s", rawGitHubBase, owner, repo, ref, filePath)
	body, status, err := r.fetchWithRetry(ctx, url)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("Failed to fetch %s: %d", filePath, status)
	}
	return string(body), nil
}

func (r *Registry) getDefaultBranch(ctx context.Context, owner, repo string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s", githubAPIBase, owner, repo)
	body, status, err := r.fetchWithRetry(ctx, url)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API error: %d", status)
	}
	var data struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}
	if data.DefaultBranch == "" {
		return "main", nil
	}
	return data.DefaultBranch, nil
}

func (r *Registry) SearchSkills(ctx context.Context, query string) []SearchResult {
	var results []SearchResult
	searchURL := fmt.Sprintf("%s/search/repositories?q=%s&per_page=%d", githubAPIBase, url.QueryEscape("skill "+query), maxSearchResult)
	body, status, err := r.fetchWithRetry(ctx, searchURL)
	if err != nil || status >= 400 {
		return results
	}
	var data struct {
		Items []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			HTMLURL     string `json:"html_url"`
			Owner       struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return results
	}
	for _, item := range data.Items {
		if !strings.Contains(strings.ToLower(item.Name), "skill") && !strings.Contains(strings.ToLower(item.Description), "skill") {
			continue
		}
		summary := item.Description
		if len(summary) > 200 {
			summary = summary[:200]
		}
		results = append(results, SearchResult{
			Slug:        item.Owner.Login + "/" + item.Name,
			DisplayName: item.Name,
			Summary:     summary,
			URL:         item.HTMLURL,
		})
	}
	return results
}

func (r *Registry) InstallFromGitHub(ctx context.Context, chatID int64, input string) InstallResult {
	ref := parseGitHubRef(input)
	if ref == nil {
		return InstallResult{Error: "URL GitHub tidak valid"}
	}
	if !ref.ExplicitRef {
		branch, err := r.getDefaultBranch(ctx, ref.Owner, ref.Repo)
		if err != nil {
			return InstallResult{Success: false, Error: "Gagal resolve branch: " + err.Error()}
		}
		ref.Ref = branch
	}

	filePaths, err := r.fetchGitHubTree(ctx, ref.Owner, ref.Repo, ref.Ref)
	if err != nil {
		return InstallResult{Success: false, Error: "Gagal install skill: " + err.Error()}
	}

	filtered := filePaths
	if ref.SubPath != "" {
		filtered = nil
		for _, p := range filePaths {
			if p == ref.SubPath || strings.HasPrefix(p, ref.SubPath+"/") {
				filtered = append(filtered, p)
			}
		}
	}

	skillMdPath := ""
	for _, p := range filtered {
		if base := p[lastSlash(p)+1:]; base == skillMarkdown {
			skillMdPath = p
			break
		}
	}
	if skillMdPath == "" {
		return InstallResult{Success: false, Error: "SKILL.md tidak ditemukan di repository"}
	}

	skillRoot := ""
	if last := lastSlash(skillMdPath); last >= 0 {
		skillRoot = skillMdPath[:last]
	}

	var rootPaths []string
	if skillRoot != "" {
		for _, p := range filtered {
			if strings.HasPrefix(p, skillRoot+"/") {
				rootPaths = append(rootPaths, p)
			}
		}
	} else {
		rootPaths = filtered
	}

	skillMdContent, err := r.fetchRawFile(ctx, ref.Owner, ref.Repo, ref.Ref, skillMdPath)
	if err != nil {
		return InstallResult{Success: false, Error: "Gagal install skill: " + err.Error()}
	}
	metadata := NewCatalog(r.VFS).ParseFrontmatter(skillMdContent)
	skillName := metadata.Name
	if skillName == "" {
		skillName = ref.Repo
	}
	if msg := ValidateSkillName(skillName); msg != "" {
		return InstallResult{Success: false, Error: msg}
	}
	if _, ok := r.VFS.ReadFile(ctx, chatID, "skills/"+skillName+"/SKILL.md"); ok {
		return InstallResult{Success: false, Error: fmt.Sprintf("Skill %q sudah terinstall", skillName)}
	}

	for _, filePath := range rootPaths {
		content, err := r.fetchRawFile(ctx, ref.Owner, ref.Repo, ref.Ref, filePath)
		if err != nil {
			return InstallResult{Success: false, Error: "Gagal install skill: " + err.Error()}
		}
		relative := filePath
		if skillRoot != "" {
			relative = filePath[len(skillRoot)+1:]
		}
		if err := r.VFS.WriteFile(ctx, chatID, "skills/"+skillName+"/"+relative, content); err != nil {
			return InstallResult{Success: false, Error: "Gagal install skill: " + err.Error()}
		}
	}
	return InstallResult{Success: true, Name: skillName, Path: "skills/" + skillName + "/SKILL.md"}
}

func (r *Registry) InstallFromContent(ctx context.Context, chatID int64, name, description, body string) InstallResult {
	if msg := ValidateSkillName(name); msg != "" {
		return InstallResult{Success: false, Error: msg}
	}
	if _, ok := r.VFS.ReadFile(ctx, chatID, "skills/"+name+"/SKILL.md"); ok {
		return InstallResult{Success: false, Error: fmt.Sprintf("Skill %q sudah terinstall", name)}
	}
	content := BuildSkillContent(name, description, body, "", nil)
	skillPath := "skills/" + name + "/SKILL.md"
	if err := r.VFS.WriteFile(ctx, chatID, skillPath, content); err != nil {
		return InstallResult{Success: false, Error: err.Error()}
	}
	return InstallResult{Success: true, Name: name, Path: skillPath}
}

func (r *Registry) UpdateSkill(ctx context.Context, chatID int64, name, description, body string) InstallResult {
	if msg := ValidateSkillName(name); msg != "" {
		return InstallResult{Success: false, Error: msg}
	}
	if _, ok := r.VFS.ReadFile(ctx, chatID, "skills/"+name+"/SKILL.md"); !ok {
		return InstallResult{Success: false, Error: fmt.Sprintf("Skill %q tidak ditemukan", name)}
	}
	content := BuildSkillContent(name, description, body, "", nil)
	skillPath := "skills/" + name + "/SKILL.md"
	if err := r.VFS.WriteFile(ctx, chatID, skillPath, content); err != nil {
		return InstallResult{Success: false, Error: err.Error()}
	}
	return InstallResult{Success: true, Name: name, Path: skillPath}
}

func (r *Registry) MigrateOldSkills(ctx context.Context, chatID int64) (migrated int, errors []string) {
	cat := NewCatalog(r.VFS)
	entries := r.VFS.ListDirectory(ctx, chatID, "skills")
	for _, entry := range entries {
		if entry.Name == "" || entry.Type != "file" || !strings.HasSuffix(entry.Name, ".md") {
			continue
		}
		oldName := strings.TrimSuffix(entry.Name, ".md")
		oldPath := "skills/" + entry.Name
		newPath := "skills/" + oldName + "/SKILL.md"
		content, ok := r.VFS.ReadFile(ctx, chatID, oldPath)
		if !ok {
			continue
		}
		if _, ok := r.VFS.ReadFile(ctx, chatID, newPath); ok {
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
		if err := r.VFS.WriteFile(ctx, chatID, newPath, newContent); err != nil {
			errors = append(errors, err.Error())
			continue
		}
		if _, err := r.VFS.DeleteFile(ctx, chatID, oldPath); err != nil {
			errors = append(errors, err.Error())
			continue
		}
		migrated++
	}
	return migrated, errors
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

func sleepMS(ctx context.Context, ms int) {
	t := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

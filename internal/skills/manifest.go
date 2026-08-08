// Package skills implements SKILL.md manifest parsing, listing and loading
// from the per-user VFS, mirroring the old src/skills-loader.ts.
package skills

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/purujawa/puru-ai/internal/vfs"
)

type SkillMetadata struct {
	Name        string
	Description string
	Homepage    string
	Metadata    map[string]any
}

type SkillInfo struct {
	Name        string
	Path        string
	Description string
	Homepage    string
	Metadata    map[string]any
}

type Catalog struct {
	vfs *vfs.VFS
}

func NewCatalog(v *vfs.VFS) *Catalog { return &Catalog{vfs: v} }

const (
	maxNameLength  = 64
	maxDescription = 1024
)

var namePatternOK = isNamePattern

// isNamePattern validates '^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$'.
func isNamePattern(s string) bool {
	if s == "" {
		return false
	}
	prevDash := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '-':
			if i == 0 {
				return false
			}
			prevDash = true
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
			prevDash = false
		default:
			return false
		}
	}
	return !prevDash
}

// ValidateSkillName returns an error message or empty string when valid.
func ValidateSkillName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "Nama skill harus diisi"
	}
	if len(trimmed) > maxNameLength {
		return "Nama skill maksimal 64 karakter"
	}
	if !namePatternOK(trimmed) {
		return "Nama skill hanya boleh berisi huruf, angka, dan hyphen"
	}
	return ""
}

// SplitFrontmatter splits YAML frontmatter ('---' delimited) from the body.
func SplitFrontmatter(content string) (frontmatter, body string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", content
	}
	endIndex := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIndex = i
			break
		}
	}
	if endIndex < 0 {
		return "", content
	}
	frontmatter = strings.Join(lines[1:endIndex], "\n")
	body = strings.TrimLeft(strings.Join(lines[endIndex+1:], "\n"), " \t\n\r")
	return frontmatter, body
}

// ParseSimpleYAML parses simple "key: value" lines (quotes stripped).
func ParseSimpleYAML(yamlContent string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(yamlContent, "\n") {
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if val == "" || !isWordKey(key) {
			continue
		}
		val = strings.Trim(val, `"'`)
		result[key] = val
	}
	return result
}

func isWordKey(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func (c *Catalog) ParseFrontmatter(content string) SkillMetadata {
	frontmatter, body := SplitFrontmatter(content)

	var name, description string
	// Directory-like semantics from the TS parser: look for '# title' first.
	title := firstHeadingTitle(body)
	if title != "" && isNamePattern(title) && len(title) <= maxNameLength {
		name = title
	}
	desc := firstDescription(body)
	if desc != "" {
		description = firstLine(desc)
	}

	metadata := SkillMetadata{}
	if frontmatter != "" {
		yamlMeta := ParseSimpleYAML(frontmatter)
		if n, ok := yamlMeta["name"]; ok && isNamePattern(n) {
			name = n
		}
		if d, ok := yamlMeta["description"]; ok {
			description = d
		}
		metadata.Homepage = yamlMeta["homepage"]
		if m, ok := yamlMeta["metadata"]; ok {
			if parsed := tryJSON(m); parsed != nil {
				metadata.Metadata = parsed
			}
		}
	}
	metadata.Name = name
	metadata.Description = description
	return metadata
}

func firstHeadingTitle(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func firstDescription(body string) string {
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines)-1; i++ {
		if strings.HasPrefix(lines[i], "# ") {
			return lines[i+1]
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		return s[:i]
	}
	return s
}

func (c *Catalog) ListSkills(ctx context.Context, chatID int64) []SkillInfo {
	var skills []SkillInfo
	seen := map[string]bool{}
	entries := c.vfs.ListDirectory(ctx, chatID, "skills")
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		var content string
		var skillPath string
		found := false
		if entry.Type == "dir" {
			skillPath = "skills/" + name + "/SKILL.md"
			content, found = c.vfs.ReadFile(ctx, chatID, skillPath)
		} else if entry.Type == "file" && strings.HasSuffix(name, ".md") {
			skillPath = "skills/" + name
			content, found = c.vfs.ReadFile(ctx, chatID, skillPath)
		}
		if !found {
			continue
		}
		metadata := c.ParseFrontmatter(content)
		skillName := metadata.Name
		if skillName == "" {
			skillName = strings.TrimSuffix(name, ".md")
		}
		if seen[skillName] {
			continue
		}
		seen[skillName] = true
		if len(metadata.Description) > maxDescription {
			metadata.Description = metadata.Description[:maxDescription] + "..."
		}
		skills = append(skills, SkillInfo{
			Name:        skillName,
			Path:        skillPath,
			Description: metadata.Description,
			Homepage:    metadata.Homepage,
			Metadata:    metadata.Metadata,
		})
	}
	return skills
}

func (c *Catalog) LoadSkill(ctx context.Context, chatID int64, name string) (string, bool) {
	if ValidateSkillName(name) != "" {
		return "", false
	}
	if content, ok := c.vfs.ReadFile(ctx, chatID, "skills/"+name+"/SKILL.md"); ok {
		_, body := SplitFrontmatter(content)
		return body, true
	}
	if content, ok := c.vfs.ReadFile(ctx, chatID, "skills/"+name+".md"); ok {
		_, body := SplitFrontmatter(content)
		return body, true
	}
	return "", false
}

func (c *Catalog) LoadSkillWithMetadata(ctx context.Context, chatID int64, name string) (string, SkillMetadata, bool) {
	if ValidateSkillName(name) != "" {
		return "", SkillMetadata{}, false
	}
	skillPath := "skills/" + name + "/SKILL.md"
	content, ok := c.vfs.ReadFile(ctx, chatID, skillPath)
	if !ok {
		skillPath = "skills/" + name + ".md"
		content, ok = c.vfs.ReadFile(ctx, chatID, skillPath)
	}
	if !ok {
		return "", SkillMetadata{}, false
	}
	metadata := c.ParseFrontmatter(content)
	_, body := SplitFrontmatter(content)
	return body, metadata, true
}

func (c *Catalog) ListSkillFiles(ctx context.Context, chatID int64, name string) []string {
	if ValidateSkillName(name) != "" {
		return nil
	}
	entries := c.vfs.ListDirectory(ctx, chatID, "skills/"+name)
	if len(entries) > 0 {
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.Name != "" {
				out = append(out, e.Name)
			}
		}
		return out
	}
	if _, ok := c.vfs.ReadFile(ctx, chatID, "skills/"+name+".md"); ok {
		return []string{name + ".md"}
	}
	return nil
}

func (c *Catalog) BuildSkillsSummary(ctx context.Context, chatID int64) string {
	skills := c.ListSkills(ctx, chatID)
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<skills>")
	for _, skill := range skills {
		sb.WriteString("\n  <skill>")
		sb.WriteString("\n    <name>" + escapeXML(skill.Name) + "</name>")
		sb.WriteString("\n    <description>" + escapeXML(skill.Description) + "</description>")
		sb.WriteString("\n    <location>" + escapeXML(skill.Path) + "</location>")
		if skill.Homepage != "" {
			sb.WriteString("\n    <homepage>" + escapeXML(skill.Homepage) + "</homepage>")
		}
		sb.WriteString("\n  </skill>")
	}
	sb.WriteString("\n</skills>")
	return sb.String()
}

func (c *Catalog) DeleteSkill(ctx context.Context, chatID int64, name string) (bool, error) {
	if ValidateSkillName(name) != "" {
		return false, nil
	}
	dirEntries := c.vfs.ListDirectory(ctx, chatID, "skills/"+name)
	deleted := false
	for _, e := range dirEntries {
		if e.Name == "" {
			continue
		}
		result, err := c.vfs.DeleteFile(ctx, chatID, "skills/"+name+"/"+e.Name)
		if err != nil {
			return false, err
		}
		if result {
			deleted = true
		}
	}
	if deleted {
		_, err := c.vfs.DeleteFile(ctx, chatID, "skills/"+name)
		return true, err
	}
	if content, ok := c.vfs.ReadFile(ctx, chatID, "skills/"+name+".md"); ok {
		_ = content
		_, err := c.vfs.DeleteFile(ctx, chatID, "skills/"+name+".md")
		return true, err
	}
	return false, nil
}

func (c *Catalog) SkillExists(ctx context.Context, chatID int64, name string) (bool, error) {
	if _, ok := c.vfs.ReadFile(ctx, chatID, "skills/"+name+"/SKILL.md"); ok {
		return true, nil
	}
	_, ok := c.vfs.ReadFile(ctx, chatID, "skills/"+name+".md")
	return ok, nil
}

func BuildSkillContent(name, description, body string, homepage string, metadata map[string]any) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: " + name + "\n")
	sb.WriteString("description: \"" + strings.ReplaceAll(description, `"`, `\"`) + "\"\n")
	if homepage != "" {
		sb.WriteString("homepage: " + homepage + "\n")
	}
	if metadata != nil {
		if b, err := json.Marshal(metadata); err == nil {
			sb.WriteString("metadata: " + string(b) + "\n")
		}
	}
	sb.WriteString("---\n\n")
	sb.WriteString("# " + name + "\n\n")
	sb.WriteString(body)
	return sb.String()
}

func escapeXML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

func tryJSON(s string) map[string]any {
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) == nil {
		return m
	}
	return nil
}

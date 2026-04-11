package handlers

// Engine HTTP handlers for brain, capabilities, data, and sandbox operations.

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kocort/kocort/api/service"
	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/skill"
	"github.com/kocort/kocort/runtime"
)

// Engine holds dependencies for engine handlers.
type Engine struct {
	Runtime *runtime.Runtime
}

type browseDirRequest struct {
	Prompt string `json:"prompt"`
}

// Brain handles GET /api/engine/brain.
func (h *Engine) Brain(c *gin.Context) {
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainSave handles POST /api/engine/brain/save.
func (h *Engine) BrainSave(c *gin.Context) {
	var req types.BrainSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		sec := service.ConfigSections{}
		if req.Agents != nil {
			cfg.Agents = *req.Agents
			sec.Main = true
		}
		if req.Models != nil {
			cfg.Models = *req.Models
			sec.Models = true
		}
		if req.SystemPrompt != nil {
			service.SetDefaultSystemPrompt(cfg, *req.SystemPrompt)
			sec.Main = true
		}
		return sec, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainModelUpsert handles POST /api/engine/brain/models/upsert.
func (h *Engine) BrainModelUpsert(c *gin.Context) {
	var req types.BrainModelUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// For OAuth presets, inject the stored access token as the API key.
	if req.APIKey == "" && req.PresetID != "" {
		if cred := service.GetOAuthCredential(h.Runtime, req.PresetID); cred != nil {
			req.APIKey = cred.AccessToken
		}
	}
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		if err := service.UpsertBrainModelRecord(cfg, req); err != nil {
			return service.ConfigSections{}, err
		}
		return service.ConfigSections{Main: true, Models: true}, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainModelDelete handles POST /api/engine/brain/models/delete.
func (h *Engine) BrainModelDelete(c *gin.Context) {
	var req types.BrainModelDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		if err := service.DeleteBrainModelRecord(cfg, req.ProviderID, req.ModelID); err != nil {
			return service.ConfigSections{}, err
		}
		return service.ConfigSections{Main: true, Models: true}, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainModelSetDefault handles POST /api/engine/brain/models/default.
func (h *Engine) BrainModelSetDefault(c *gin.Context) {
	var req types.BrainModelAssignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		if err := service.SetBrainModelDefault(cfg, req.ProviderID, req.ModelID); err != nil {
			return service.ConfigSections{}, err
		}
		return service.ConfigSections{Main: true}, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainModelSetFallback handles POST /api/engine/brain/models/fallback.
func (h *Engine) BrainModelSetFallback(c *gin.Context) {
	var req types.BrainModelAssignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		if err := service.SetBrainModelFallback(cfg, req.ProviderID, req.ModelID, enabled); err != nil {
			return service.ConfigSections{}, err
		}
		return service.ConfigSections{Main: true}, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// Capabilities handles GET /api/engine/capabilities.
func (h *Engine) Capabilities(c *gin.Context) {
	c.JSON(http.StatusOK, service.BuildCapabilitiesState(c.Request.Context(), h.Runtime))
}

// CapabilitiesSave handles POST /api/engine/capabilities/save.
func (h *Engine) CapabilitiesSave(c *gin.Context) {
	var req types.CapabilitiesSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		if req.Skills != nil {
			service.ApplySkillToggles(cfg, req.Skills)
		}
		if req.Plugins != nil {
			cfg.Plugins = *req.Plugins
		}
		if len(req.ToolToggles) > 0 {
			service.ApplyToolToggles(cfg, req.ToolToggles)
		}
		return service.ConfigSections{Main: true}, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.HeartbeatsEnabled != nil {
		h.Runtime.SetHeartbeatsEnabled(*req.HeartbeatsEnabled)
	}
	c.JSON(http.StatusOK, service.BuildCapabilitiesState(c.Request.Context(), h.Runtime))
}

// SkillInstall handles POST /api/engine/capabilities/skill/install.
func (h *Engine) SkillInstall(c *gin.Context) {
	var req types.SkillInstallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	skillName := strings.TrimSpace(req.SkillName)
	if skillName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "skillName is required"})
		return
	}
	identity, err := service.ResolveDefaultIdentityPublic(c.Request.Context(), h.Runtime)
	if err != nil || strings.TrimSpace(identity.WorkspaceDir) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot resolve workspace directory"})
		return
	}
	result, err := skill.InstallSkill(c.Request.Context(), skill.SkillInstallRequest{
		WorkspaceDir: identity.WorkspaceDir,
		SkillName:    skillName,
		InstallID:    strings.TrimSpace(req.InstallID),
		Config:       &h.Runtime.Config,
		Timeout:      time.Duration(req.TimeoutMs) * time.Millisecond,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// Data handles GET /api/engine/data.
func (h *Engine) Data(c *gin.Context) {
	c.JSON(http.StatusOK, service.BuildDataState(c.Request.Context(), h.Runtime))
}

// DataSave handles POST /api/engine/data/save.
func (h *Engine) DataSave(c *gin.Context) {
	var req types.DataSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		if err := service.SaveDataState(cfg, req); err != nil {
			return service.ConfigSections{}, err
		}
		return service.ConfigSections{Main: true}, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildDataState(c.Request.Context(), h.Runtime))
}

// Sandbox handles GET /api/engine/sandbox.
func (h *Engine) Sandbox(c *gin.Context) {
	c.JSON(http.StatusOK, service.BuildSandboxState(h.Runtime))
}

// SandboxSave handles POST /api/engine/sandbox/save.
func (h *Engine) SandboxSave(c *gin.Context) {
	var req types.SandboxSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		for _, patch := range req.Agents {
			agentID := session.NormalizeAgentID(patch.AgentID)
			if agentID == "" {
				continue
			}
			sandboxDirs := patch.SandboxDirs
			sandboxEnabled := patch.SandboxEnabled
			applied := false
			for i := range cfg.Agents.List {
				if session.NormalizeAgentID(cfg.Agents.List[i].ID) != agentID {
					continue
				}
				cfg.Agents.List[i].SandboxEnabled = sandboxEnabled
				cfg.Agents.List[i].SandboxDirs = sandboxDirs
				applied = true
				break
			}
			if cfg.Agents.Defaults != nil && config.ResolveDefaultConfiguredAgentID(*cfg) == agentID {
				cfg.Agents.Defaults.SandboxEnabled = sandboxEnabled
				cfg.Agents.Defaults.SandboxDirs = sandboxDirs
				applied = true
			}
			if !applied {
				cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
					ID:             agentID,
					SandboxEnabled: sandboxEnabled,
					SandboxDirs:    sandboxDirs,
				})
			}
		}
		return service.ConfigSections{Main: true}, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildSandboxState(h.Runtime))
}

// SkillFiles handles GET /api/engine/capabilities/skill/files?baseDir=...
// Returns the list of files within the skill directory.
func (h *Engine) SkillFiles(c *gin.Context) {
	baseDir := strings.TrimSpace(c.Query("baseDir"))
	if baseDir == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "baseDir is required"})
		return
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid baseDir"})
		return
	}
	info, err := os.Stat(absBase)
	if err != nil || !info.IsDir() {
		c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
		return
	}
	var files []gin.H
	_ = filepath.Walk(absBase, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if fi.IsDir() {
			name := fi.Name()
			if name == ".git" || name == "node_modules" || name == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(absBase, path)
		if relErr != nil {
			return nil
		}
		files = append(files, gin.H{
			"name": rel,
			"size": fi.Size(),
		})
		return nil
	})
	c.JSON(http.StatusOK, gin.H{"files": files})
}

// SkillFileRead handles GET /api/engine/capabilities/skill/file?baseDir=...&file=...
// Returns the content of a single file within the skill directory.
func (h *Engine) SkillFileRead(c *gin.Context) {
	baseDir := strings.TrimSpace(c.Query("baseDir"))
	file := strings.TrimSpace(c.Query("file"))
	if baseDir == "" || file == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "baseDir and file are required"})
		return
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid baseDir"})
		return
	}
	target := filepath.Join(absBase, file)
	absTarget, err := filepath.Abs(target)
	if err != nil || !pathWithinBase(absTarget, absBase) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file path"})
		return
	}
	data, err := os.ReadFile(absTarget)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"name": file, "content": string(data)})
}

func pathWithinBase(path string, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// SkillImportValidate handles POST /api/engine/capabilities/skill/import/validate.
// Accepts a multipart form with either a "zip" file or a "dir" path field.
// Returns validation result: skill name, description, and whether it's valid.
func (h *Engine) SkillImportValidate(c *gin.Context) {
	dirPath := strings.TrimSpace(c.PostForm("dir"))
	if dirPath != "" {
		h.validateSkillDir(c, dirPath)
		return
	}
	file, header, err := c.Request.FormFile("zip")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provide either a 'zip' file or a 'dir' path"})
		return
	}
	defer file.Close()

	// Save to temp dir, extract, and validate
	tmpDir, err := os.MkdirTemp("", "skill-import-*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create temp directory"})
		return
	}

	zipPath := filepath.Join(tmpDir, header.Filename)
	out, err := os.Create(zipPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save uploaded file"})
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		os.RemoveAll(tmpDir)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write uploaded file"})
		return
	}
	out.Close()

	extractDir := filepath.Join(tmpDir, "extracted")
	if err := extractZip(zipPath, extractDir); err != nil {
		os.RemoveAll(tmpDir)
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("failed to extract zip: %v", err)})
		return
	}

	skillDir, valid, info := findSkillRoot(extractDir)
	if !valid {
		os.RemoveAll(tmpDir)
		c.JSON(http.StatusOK, gin.H{"valid": false, "error": "no SKILL.md found in the archive"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"valid":       true,
		"name":        info["name"],
		"description": info["description"],
		"skillDir":    skillDir,
		"tempDir":     tmpDir,
		"source":      "zip",
	})
}

// SkillImportConfirm handles POST /api/engine/capabilities/skill/import/confirm.
// Copies the validated skill to the workspace skills directory and enables it.
func (h *Engine) SkillImportConfirm(c *gin.Context) {
	var req struct {
		SkillDir string `json:"skillDir"`
		TempDir  string `json:"tempDir,omitempty"`
		Source   string `json:"source"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.TempDir != "" {
		defer os.RemoveAll(req.TempDir)
	}
	if strings.TrimSpace(req.SkillDir) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "skillDir is required"})
		return
	}

	// Resolve workspace directory
	identity, err := service.ResolveDefaultIdentityPublic(c.Request.Context(), h.Runtime)
	if err != nil || strings.TrimSpace(identity.WorkspaceDir) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot resolve workspace directory"})
		return
	}
	workspaceDir := identity.WorkspaceDir

	// Determine skill name from SKILL.md
	skillMd := filepath.Join(req.SkillDir, skill.DefaultSkillFilename)
	content, err := os.ReadFile(skillMd)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid skill directory: SKILL.md not found"})
		return
	}
	skillName := parseSkillName(string(content))
	if skillName == "" {
		skillName = filepath.Base(req.SkillDir)
	}

	// Copy skill to workspace skills directory
	targetDir := filepath.Join(workspaceDir, "skills", sanitizeDirName(skillName))
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create skills directory"})
		return
	}
	if _, err := os.Stat(targetDir); err == nil {
		// Target exists — remove it first (overwrite)
		os.RemoveAll(targetDir)
	}

	if req.Source == "dir" {
		// Copy directory
		if err := copyDir(req.SkillDir, targetDir); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to copy skill: %v", err)})
			return
		}
	} else {
		// Move from temp extracted dir
		if err := copyDir(req.SkillDir, targetDir); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to copy skill: %v", err)})
			return
		}
	}

	// Enable the skill in config
	skillKey := sanitizeDirName(skillName)
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		enabled := true
		if cfg.Skills.Entries == nil {
			cfg.Skills.Entries = make(map[string]config.SkillConfigLite)
		}
		cfg.Skills.Entries[skillKey] = config.SkillConfigLite{Enabled: &enabled}
		service.ApplySkillToggles(cfg, &config.SkillsConfig{Entries: map[string]config.SkillConfigLite{
			skillKey: {Enabled: &enabled},
		}})
		return service.ConfigSections{Main: true}, nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("skill copied but failed to enable: %v", err)})
		return
	}

	c.JSON(http.StatusOK, service.BuildCapabilitiesState(c.Request.Context(), h.Runtime))
}

func (h *Engine) validateSkillDir(c *gin.Context, dirPath string) {
	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid directory path"})
		return
	}
	info, err := os.Stat(absDir)
	if err != nil || !info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"valid": false, "error": "directory not found"})
		return
	}

	skillDir, valid, skillInfo := findSkillRoot(absDir)
	if !valid {
		c.JSON(http.StatusOK, gin.H{"valid": false, "error": "no SKILL.md found in directory"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"valid":       true,
		"name":        skillInfo["name"],
		"description": skillInfo["description"],
		"skillDir":    skillDir,
		"source":      "dir",
	})
}

// findSkillRoot searches for SKILL.md in a directory tree.
// Returns the directory containing SKILL.md, validity flag, and parsed name/description.
func findSkillRoot(root string) (string, bool, map[string]string) {
	// Check root directly
	md := filepath.Join(root, skill.DefaultSkillFilename)
	if data, err := os.ReadFile(md); err == nil {
		return root, true, parseSkillInfo(string(data))
	}
	// Check immediate children
	children, err := os.ReadDir(root)
	if err != nil {
		return "", false, nil
	}
	for _, child := range children {
		if !child.IsDir() {
			continue
		}
		md := filepath.Join(root, child.Name(), skill.DefaultSkillFilename)
		if data, err := os.ReadFile(md); err == nil {
			return filepath.Join(root, child.Name()), true, parseSkillInfo(string(data))
		}
	}
	return "", false, nil
}

func parseSkillInfo(content string) map[string]string {
	info := map[string]string{"name": "", "description": ""}
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			break
		}
		if !inFrontmatter {
			continue
		}
		if strings.HasPrefix(trimmed, "name:") {
			info["name"] = strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
		}
		if strings.HasPrefix(trimmed, "description:") {
			info["description"] = strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))
		}
	}
	return info
}

func parseSkillName(content string) string {
	return parseSkillInfo(content)["name"]
}

func sanitizeDirName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, name)
	// Collapse consecutive dashes
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	return strings.Trim(name, "-")
}

func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name)
		absTarget, err := filepath.Abs(target)
		if err != nil || !strings.HasPrefix(absTarget, destDir) {
			continue // skip path traversal
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(absTarget, 0o755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(absTarget), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		outFile, err := os.Create(absTarget)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// SkillBrowseDir handles POST /api/engine/capabilities/skill/browse-dir.
// Opens a native OS directory picker dialog and returns the selected path.
func (h *Engine) SkillBrowseDir(c *gin.Context) {
	selected, err := openNativeDirPicker("Select skill directory")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"path": "", "cancelled": true})
		return
	}
	selected = strings.TrimSpace(selected)
	if selected == "" {
		c.JSON(http.StatusOK, gin.H{"path": "", "cancelled": true})
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": selected, "cancelled": false})
}

// BrowseDir handles POST /api/engine/browse-dir.
// Opens a native OS directory picker dialog and returns the selected path.
func (h *Engine) BrowseDir(c *gin.Context) {
	var req browseDirRequest
	_ = c.ShouldBindJSON(&req)
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = "Select directory"
	}
	selected, err := openNativeDirPicker(prompt)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"path": "", "cancelled": true})
		return
	}
	selected = strings.TrimSpace(selected)
	if selected == "" {
		c.JSON(http.StatusOK, gin.H{"path": "", "cancelled": true})
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": selected, "cancelled": false})
}

func openNativeDirPicker(prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "Select directory"
	}
	switch goruntime.GOOS {
	case "darwin":
		out, err := exec.Command("osascript", "-e",
			fmt.Sprintf(`POSIX path of (choose folder with prompt %q)`, prompt)).Output()
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(out), "\n/"), nil
	case "linux":
		out, err := exec.Command("zenity", "--file-selection", "--directory",
			"--title="+prompt).Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	case "windows":
		psScript := `Add-Type -AssemblyName System.Windows.Forms; ` +
			`$b = New-Object System.Windows.Forms.FolderBrowserDialog; ` +
			`$b.Description = ` + fmt.Sprintf("'%s'", strings.ReplaceAll(prompt, "'", "''")) + `; ` +
			`$b.RootFolder = 'MyComputer'; ` +
			`if ($b.ShowDialog() -eq 'OK') { $b.SelectedPath }`
		out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript).Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	default:
		return "", fmt.Errorf("unsupported OS: %s", goruntime.GOOS)
	}
}

// CerebellumStart handles POST /api/engine/brain/cerebellum/start.
func (h *Engine) CerebellumStart(c *gin.Context) {
	if err := service.CerebellumStart(h.Runtime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// CerebellumStop handles POST /api/engine/brain/cerebellum/stop.
func (h *Engine) CerebellumStop(c *gin.Context) {
	if err := service.CerebellumStop(h.Runtime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// CerebellumRestart handles POST /api/engine/brain/cerebellum/restart.
func (h *Engine) CerebellumRestart(c *gin.Context) {
	if err := service.CerebellumRestart(h.Runtime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// CerebellumSelectModel handles POST /api/engine/brain/cerebellum/model.
func (h *Engine) CerebellumSelectModel(c *gin.Context) {
	var req types.CerebellumModelSelectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.CerebellumSelectModel(h.Runtime, req.ModelID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// CerebellumClearModelSelection handles POST /api/engine/brain/cerebellum/model/clear.
func (h *Engine) CerebellumClearModelSelection(c *gin.Context) {
	if err := service.CerebellumClearModelSelection(h.Runtime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// CerebellumDeleteModel handles POST /api/engine/brain/cerebellum/model/delete.
func (h *Engine) CerebellumDeleteModel(c *gin.Context) {
	var req types.LocalModelDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.CerebellumDeleteModel(h.Runtime, req.ModelID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// CerebellumDownloadModel handles POST /api/engine/brain/cerebellum/download.
// The download runs asynchronously; the response returns immediately with the
// current brain state including download progress. The frontend should poll
// GET /api/engine/brain to track download progress.
func (h *Engine) CerebellumDownloadModel(c *gin.Context) {
	var req types.CerebellumDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.CerebellumDownloadModel(h.Runtime, req.PresetID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// CerebellumCancelDownload handles POST /api/engine/brain/cerebellum/download/cancel.
func (h *Engine) CerebellumCancelDownload(c *gin.Context) {
	if err := service.CerebellumCancelDownload(h.Runtime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// CerebellumCancelLibDownload handles POST /api/engine/brain/cerebellum/download/cancel-lib.
func (h *Engine) CerebellumCancelLibDownload(c *gin.Context) {
	if err := service.CerebellumCancelLibDownload(h.Runtime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// CerebellumHelp handles POST /api/engine/brain/cerebellum/help.
func (h *Engine) CerebellumHelp(c *gin.Context) {
	var req types.CerebellumHelpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := service.CerebellumHelp(h.Runtime, req.Query, req.Context)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, types.CerebellumHelpResponse{
		Answer:     result.Answer,
		Suggestion: result.Suggestion,
	})
}

// ---------------------------------------------------------------------------
// Brain Local Model handlers
// ---------------------------------------------------------------------------

// BrainModeSwitch handles POST /api/engine/brain/mode.
func (h *Engine) BrainModeSwitch(c *gin.Context) {
	var req types.BrainModeSwitchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.BrainModeSwitch(h.Runtime, req.Mode, req.CerebellumEnabled); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainLocalStart handles POST /api/engine/brain/local/start.
func (h *Engine) BrainLocalStart(c *gin.Context) {
	if err := service.BrainLocalStart(h.Runtime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainLocalStop handles POST /api/engine/brain/local/stop.
func (h *Engine) BrainLocalStop(c *gin.Context) {
	if err := service.BrainLocalStop(h.Runtime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainLocalRestart handles POST /api/engine/brain/local/restart.
func (h *Engine) BrainLocalRestart(c *gin.Context) {
	if err := service.BrainLocalRestart(h.Runtime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainLocalSelectModel handles POST /api/engine/brain/local/model.
func (h *Engine) BrainLocalSelectModel(c *gin.Context) {
	var req types.BrainLocalModelSelectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.BrainLocalSelectModel(h.Runtime, req.ModelID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainLocalClearModelSelection handles POST /api/engine/brain/local/model/clear.
func (h *Engine) BrainLocalClearModelSelection(c *gin.Context) {
	if err := service.BrainLocalClearModelSelection(h.Runtime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainLocalDeleteModel handles POST /api/engine/brain/local/model/delete.
func (h *Engine) BrainLocalDeleteModel(c *gin.Context) {
	var req types.LocalModelDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.BrainLocalDeleteModel(h.Runtime, req.ModelID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainLocalDownloadModel handles POST /api/engine/brain/local/download.
func (h *Engine) BrainLocalDownloadModel(c *gin.Context) {
	var req types.BrainLocalDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.BrainLocalDownloadModel(h.Runtime, req.PresetID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainLocalCancelDownload handles POST /api/engine/brain/local/download/cancel.
func (h *Engine) BrainLocalCancelDownload(c *gin.Context) {
	if err := service.BrainLocalCancelDownload(h.Runtime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainLocalCancelLibDownload handles POST /api/engine/brain/local/download/cancel-lib.
func (h *Engine) BrainLocalCancelLibDownload(c *gin.Context) {
	if err := service.BrainLocalCancelLibDownload(h.Runtime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// BrainLocalUpdateParams handles POST /api/engine/brain/local/params.
func (h *Engine) BrainLocalUpdateParams(c *gin.Context) {
	var req types.LocalModelParamsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.BrainLocalUpdateParams(h.Runtime, req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// CerebellumUpdateParams handles POST /api/engine/brain/cerebellum/params.
func (h *Engine) CerebellumUpdateParams(c *gin.Context) {
	var req types.LocalModelParamsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.CerebellumUpdateParams(h.Runtime, req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildBrainState(c.Request.Context(), h.Runtime))
}

// OAuthDeviceCodeStart handles POST /api/engine/brain/oauth/start.
func (h *Engine) OAuthDeviceCodeStart(c *gin.Context) {
	var req types.OAuthDeviceCodeStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := service.OAuthDeviceCodeRequest(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// OAuthDeviceCodePoll handles POST /api/engine/brain/oauth/poll.
func (h *Engine) OAuthDeviceCodePoll(c *gin.Context) {
	var req types.OAuthDeviceCodePollRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := service.OAuthDeviceCodePoll(h.Runtime, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// OAuthStatus handles GET /api/engine/brain/oauth/status.
func (h *Engine) OAuthStatus(c *gin.Context) {
	creds, _ := service.LoadOAuthCredentials(h.Runtime)
	authenticated := map[string]bool{}
	for _, cred := range creds {
		authenticated[cred.ProviderID] = cred.AccessToken != ""
	}
	c.JSON(http.StatusOK, types.OAuthStatusResponse{
		Authenticated: authenticated,
	})
}

// OAuthLogout handles POST /api/engine/brain/oauth/logout.
func (h *Engine) OAuthLogout(c *gin.Context) {
	var req struct {
		ProviderID string `json:"providerId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.DeleteOAuthCredential(h.Runtime, req.ProviderID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// SetupStatus handles GET /api/setup/status — checks whether onboarding is needed.
func (h *Engine) SetupStatus(c *gin.Context) {
	hasModels := len(h.Runtime.Config.Models.Providers) > 0
	setupDone := h.Runtime.Config.SetupCompleted
	c.JSON(http.StatusOK, types.SetupStatusResponse{
		NeedsSetup: !setupDone && !hasModels,
		HasModels:  hasModels,
	})
}

// SetupComplete handles POST /api/setup/complete — marks onboarding as done.
func (h *Engine) SetupComplete(c *gin.Context) {
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		cfg.SetupCompleted = true
		return service.ConfigSections{Main: true}, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

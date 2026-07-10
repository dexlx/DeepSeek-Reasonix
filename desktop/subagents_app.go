package main

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/sandbox"
	"reasonix/internal/skill"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

// SubagentProfileInput is the desktop-bound shape for authoring a subagent
// profile. Named SubagentProfile* rather than bare Subagent* to stay distinct
// from internal/agent's Subagent* run-transcript types: this is a saved
// authoring profile (a skill file), not a runtime record of one execution.
//
// A profile is always written with runAs=subagent and invocation=manual — it
// stays invocable by name (/<name>, run_skill) but never enters the pinned
// Skills index the model scans for candidates to call on its own initiative
// (see internal/skill/index.go). This is deliberate: a profile authored
// through a settings form has no triggers/auto-use tuning, so nothing about
// it signals the model should discover it unprompted.
type SubagentProfileInput struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	SystemPrompt string   `json:"systemPrompt"`
	Color        string   `json:"color"`
	Model        string   `json:"model"`
	Effort       string   `json:"effort"`
	AllowedTools []string `json:"allowedTools"`
	// Scope is "project" or "global" (default when empty/unrecognized).
	Scope string `json:"scope"`
}

func subagentProfileScope(raw string) skill.Scope {
	if strings.TrimSpace(raw) == "project" {
		return skill.ScopeProject
	}
	return skill.ScopeGlobal
}

// CreateSubagentProfile writes a new user-authored subagent profile and
// returns its file path. Refuses a name that collides with a built-in
// subagent skill (explore/research/review/security-review) — Store.List's
// dedup rules let a same-named user file silently shadow the built-in
// everywhere, including the dedicated top-level explore/review tools, so this
// must be caught here rather than left to the generic CreateWithContent
// same-scope-only overwrite check.
func (a *App) CreateSubagentProfile(input SubagentProfileInput) (string, error) {
	name := strings.TrimSpace(input.Name)
	if !config.IsValidSkillName(name) {
		return "", fmt.Errorf("invalid name %q — use letters, digits, '_', '-', '.'", name)
	}
	for _, builtin := range skill.BuiltinNames() {
		if config.SkillNameKey(builtin) == config.SkillNameKey(name) {
			return "", fmt.Errorf("%q is a built-in subagent name and cannot be reused", name)
		}
	}
	desc := strings.TrimSpace(input.Description)
	if desc == "" {
		return "", fmt.Errorf("description is required")
	}
	prompt := strings.TrimSpace(input.SystemPrompt)
	if prompt == "" {
		return "", fmt.Errorf("system prompt is required")
	}

	_, ctrl := a.activeTabAndCtrl()
	if ctrl == nil {
		return "", fmt.Errorf("no active session")
	}
	for _, existing := range ctrl.AllSkills() {
		if config.SkillNameKey(existing.Name) == config.SkillNameKey(name) {
			return "", fmt.Errorf("%q already exists", name)
		}
	}

	content := skill.RenderSkillFile(skill.SkillFileOptions{
		Name:         name,
		Description:  desc,
		Body:         prompt,
		RunAs:        skill.RunSubagent,
		Model:        strings.TrimSpace(input.Model),
		Effort:       strings.TrimSpace(input.Effort),
		AllowedTools: input.AllowedTools,
		Color:        strings.TrimSpace(input.Color),
		Invocation:   "manual",
	})
	path, err := ctrl.CreateSkill(name, subagentProfileScope(input.Scope), content)
	if err != nil {
		return "", err
	}
	// Mirrors RefreshSkills/SetSkillEnabled: degrade a lease-held rebuild to a
	// deferred warning (the file is already saved), fail hard on a real error.
	if err := a.RefreshSkills(); err != nil {
		return "", err
	}
	return path, nil
}

// UpdateSubagentProfile overwrites an existing user-authored subagent
// profile's content in place. name and scope are the profile's identity and
// are not editable through this call — the frontend keeps them read-only in
// the edit form, since renaming or re-scoping would mean moving the file
// (delete-then-create), a separate operation this repo doesn't support yet.
// input.Name/input.Scope are ignored in favor of the name/scope params.
func (a *App) UpdateSubagentProfile(name, scope string, input SubagentProfileInput) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	desc := strings.TrimSpace(input.Description)
	if desc == "" {
		return fmt.Errorf("description is required")
	}
	prompt := strings.TrimSpace(input.SystemPrompt)
	if prompt == "" {
		return fmt.Errorf("system prompt is required")
	}

	_, ctrl := a.activeTabAndCtrl()
	if ctrl == nil {
		return fmt.Errorf("no active session")
	}

	content := skill.RenderSkillFile(skill.SkillFileOptions{
		Name:         name,
		Description:  desc,
		Body:         prompt,
		RunAs:        skill.RunSubagent,
		Model:        strings.TrimSpace(input.Model),
		Effort:       strings.TrimSpace(input.Effort),
		AllowedTools: input.AllowedTools,
		Color:        strings.TrimSpace(input.Color),
		Invocation:   "manual",
	})
	if err := ctrl.UpdateSkill(name, subagentProfileScope(scope), content); err != nil {
		return err
	}
	if err := a.RefreshSkills(); err != nil {
		return err
	}
	return nil
}

// DeleteSubagentProfile removes a user-authored subagent profile. scope must
// match what the caller most recently saw for this name (SkillView.Scope) —
// Store.Delete refuses a scope mismatch rather than guessing, so a stale
// client-side scope fails safely instead of deleting the wrong file.
func (a *App) DeleteSubagentProfile(name, scope string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	_, ctrl := a.activeTabAndCtrl()
	if ctrl == nil {
		return fmt.Errorf("no active session")
	}
	if err := ctrl.DeleteSkill(name, subagentProfileScope(scope)); err != nil {
		return err
	}
	if err := a.RefreshSkills(); err != nil {
		return err
	}
	return nil
}

// TrySubagentProfile runs a subagent profile once, synchronously, fully
// isolated from any live session — it builds its own provider and tool
// registry straight from config, exactly like the standalone `reasonix
// review` CLI command (internal/cli/review.go) does, and never touches
// Controller.RunSkill or any part of the Chat Runtime critical path. Because
// it needs nothing saved to disk, it runs directly against the caller's
// current form values (input), so a profile can be tried before Save.
func (a *App) TrySubagentProfile(input SubagentProfileInput, task string) (string, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	prompt := strings.TrimSpace(input.SystemPrompt)
	if prompt == "" {
		return "", fmt.Errorf("system prompt is required")
	}

	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	modelRef := strings.TrimSpace(input.Model)
	if modelRef == "" {
		modelRef = strings.TrimSpace(cfg.Agent.SubagentModel)
	}
	if modelRef == "" {
		modelRef = cfg.DefaultModel
	}
	entry, ok := cfg.ResolveModel(modelRef)
	if !ok {
		return "", fmt.Errorf("unknown model %q", modelRef)
	}
	me := *entry
	if effort := strings.TrimSpace(input.Effort); effort != "" {
		normalized, err := config.NormalizeEffort(&me, effort)
		if err != nil {
			return "", err
		}
		me.Effort = normalized
		if me.Kind == "anthropic" && me.Effort != "" && strings.TrimSpace(me.Thinking) == "" {
			me.Thinking = "adaptive"
		}
	}
	prov, err := boot.NewProviderWithProxy(&me, cfg.NetworkProxySpec())
	if err != nil {
		return "", err
	}

	root := ""
	if tab, _ := a.activeTabAndCtrl(); tab != nil {
		root = tab.WorkspaceRoot
	}
	parentReg := tool.NewRegistry()
	for _, tl := range tool.Builtins() {
		parentReg.Add(tl)
	}
	if _, ok := parentReg.Get("bash"); ok {
		bashSpec := sandbox.Spec{
			Mode:            cfg.BashMode(),
			WriteRoots:      cfg.WriteRootsForRoot(root),
			ForbidReadRoots: cfg.ForbidReadRootsForRoot(root),
			Network:         cfg.Sandbox.Network,
		}
		guard := builtin.NewSessionDataGuard(config.MemoryUserDir(), cfg.AllowWriteRoots())
		parentReg.Add(builtin.ConfineBash(bashSpec, guard))
	}
	// SubagentToolRegistry treats an empty input.AllowedTools as "all" (the
	// "default all permissions" tool-scope option), matching how a saved
	// profile's runtime dispatch already behaves.
	reg := agent.SubagentToolRegistry(parentReg, input.AllowedTools)

	result, err := agent.RunSubAgentWithSession(context.Background(), prov, reg, agent.NewSession(prompt), task, agent.Options{
		MaxSteps:      12,
		Temperature:   cfg.Agent.Temperature,
		Pricing:       me.Price,
		ContextWindow: me.ContextWindow,
	}, event.Discard)
	if err != nil {
		return "", err
	}
	return result, nil
}

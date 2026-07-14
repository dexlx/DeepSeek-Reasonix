package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/provider"
	_ "reasonix/internal/provider/anthropic"
	_ "reasonix/internal/provider/openai"
	"reasonix/internal/repair"
)

func runAssist(args []string) int {
	fs := flag.NewFlagSet("reasonix-guard assist", flag.ContinueOnError)
	root := fs.String("root", ".", "project root to diagnose")
	model := fs.String("model", "", "provider or provider/model used for the one-shot plan")
	network := fs.Bool("network", true, "include provider connectivity diagnostics")
	apply := fs.Bool("apply", false, "apply the validated plan after preview and confirmation")
	yes := fs.Bool("yes", false, "confirm plan application non-interactively")
	allowProject := fs.Bool("allow-project", false, "allow a plan to quarantine project reasonix.toml")
	jsonOut := fs.Bool("json", false, "print the plan and preview as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	report, err := repair.Diagnose(ctx, repair.DiagnoseOptions{Root: *root, Network: *network})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	plan, err := requestRepairPlan(ctx, *root, *model, report)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	opts := repair.ApplyPlanOptions{Root: *root, AllowProject: *allowProject}
	preview, err := repair.PreviewRepairPlan(plan, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if *jsonOut {
		if code := printJSON(struct {
			Plan    repair.RepairPlan          `json:"plan"`
			Preview []repair.RepairPlanPreview `json:"preview"`
		}{plan, preview}); code != 0 {
			return code
		}
	} else {
		printPlanPreview(plan, preview)
	}
	if !*apply {
		return 0
	}
	if !*yes && !confirmPlan() {
		fmt.Println("repair plan not applied")
		return 0
	}
	result, err := repair.ApplyRepairPlan(plan, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if *jsonOut {
		return printJSON(result)
	}
	for _, action := range result.Applied {
		fmt.Println("applied:", action)
	}
	return 0
}

func runApplyPlan(args []string) int {
	fs := flag.NewFlagSet("reasonix-guard apply-plan", flag.ContinueOnError)
	root := fs.String("root", ".", "project root")
	file := fs.String("file", "", "RepairPlan JSON file")
	yes := fs.Bool("yes", false, "confirm plan application non-interactively")
	allowProject := fs.Bool("allow-project", false, "allow project reasonix.toml repair")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || strings.TrimSpace(*file) == "" {
		return 2
	}
	b, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	plan, err := repair.DecodeRepairPlan(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	opts := repair.ApplyPlanOptions{Root: *root, AllowProject: *allowProject}
	preview, err := repair.PreviewRepairPlan(plan, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if !*jsonOut {
		printPlanPreview(plan, preview)
	}
	if !*yes && !confirmPlan() {
		fmt.Println("repair plan not applied")
		return 0
	}
	result, err := repair.ApplyRepairPlan(plan, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if *jsonOut {
		return printJSON(result)
	}
	for _, action := range result.Applied {
		fmt.Println("applied:", action)
	}
	return 0
}

func requestRepairPlan(ctx context.Context, root, modelRef string, report repair.DiagnosticReport) (repair.RepairPlan, error) {
	cfg, err := loadAIProviderConfig(root)
	if err != nil {
		return repair.RepairPlan{}, err
	}
	if strings.TrimSpace(modelRef) == "" {
		modelRef = cfg.DefaultModel
	}
	entry, ok := cfg.ResolveModel(modelRef)
	if !ok {
		return repair.RepairPlan{}, fmt.Errorf("AI assistance model %q is not configured", modelRef)
	}
	if entry.RequiresAPIKey() && entry.APIKey() == "" {
		return repair.RepairPlan{}, fmt.Errorf("AI assistance provider %q has no configured API key", entry.Name)
	}
	p, err := provider.New(entry.Kind, provider.Config{
		Name: entry.Name, BaseURL: entry.BaseURL, Model: entry.Model, APIKey: entry.APIKey(),
		Extra: map[string]any{
			"api_key_env": entry.APIKeyEnv, "api_key_source": entry.APIKeySourceLabel(),
			"thinking": entry.Thinking, "effort": entry.Effort, "reasoning_protocol": entry.ReasoningProtocol,
			"chat_url": entry.ChatURL, "headers": entry.Headers, "extra_body": entry.ExtraBody,
			"auth_header": entry.AuthHeader, "proxy_spec": cfg.NetworkProxySpec(),
		},
	})
	if err != nil {
		return repair.RepairPlan{}, err
	}
	safeReport := sanitizeDiagnosticReport(report)
	payload, _ := json.Marshal(safeReport)
	stream, err := p.Stream(ctx, provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: repairPlanSystemPrompt},
			{Role: provider.RoleUser, Content: string(payload)},
		},
		Temperature: provider.TemperaturePtr(0),
		MaxTokens:   2000,
	})
	if err != nil {
		return repair.RepairPlan{}, err
	}
	var out strings.Builder
	for chunk := range stream {
		switch chunk.Type {
		case provider.ChunkText:
			out.WriteString(chunk.Text)
		case provider.ChunkError:
			if chunk.Err != nil {
				return repair.RepairPlan{}, chunk.Err
			}
		}
	}
	return repair.DecodeRepairPlan([]byte(out.String()))
}

func loadAIProviderConfig(root string) (*config.Config, error) {
	if cfg, err := config.LoadForRoot(root); err == nil {
		return cfg, nil
	}
	if snapshots, err := repair.ListConfigSnapshots(); err == nil {
		for _, snapshot := range snapshots {
			cfg, loadErr := config.LoadForEditReadOnlyStrict(snapshot.Path)
			if loadErr == nil {
				return cfg, nil
			}
		}
	}
	// Built-in recovery defaults resolve the global credential file without
	// reading, migrating, or rewriting malformed user/project TOML.
	return config.LoadRecoveryDefaultsForRoot(root), nil
}

const repairPlanSystemPrompt = `You are a Reasonix recovery planner. Return JSON only, matching exactly:
{"schemaVersion":1,"summary":"...","actions":[{"type":"...","scope":"","snapshotId":"","target":"","reason":"..."}]}
Allowed actions only (return an empty actions array when no safe action applies):
- repair_config with scope global or project
- restore_snapshot with snapshotId
- rebuild_derived_state with target tabs, projects, window, zoom, or all
- rollback_update with no parameters
Never request shell commands, credential changes, session-content edits, arbitrary paths, plugin execution, or source-code changes. Prefer the smallest reversible plan supported by the diagnostics. Do not invent snapshot IDs.`

func sanitizeDiagnosticReport(report repair.DiagnosticReport) repair.DiagnosticReport {
	root := report.Root
	report.Root = "<project>"
	home, _ := os.UserHomeDir()
	for i := range report.Findings {
		for _, secretPath := range []string{root, home, config.ReasonixHomeDir(), config.MemoryUserDir()} {
			if strings.TrimSpace(secretPath) != "" {
				report.Findings[i].Message = strings.ReplaceAll(report.Findings[i].Message, secretPath, "<redacted-path>")
				report.Findings[i].Remediation = strings.ReplaceAll(report.Findings[i].Remediation, secretPath, "<redacted-path>")
			}
		}
	}
	return report
}

func printPlanPreview(plan repair.RepairPlan, previews []repair.RepairPlanPreview) {
	fmt.Println("AI repair plan:", plan.Summary)
	for _, preview := range previews {
		fmt.Printf("  %d. %s\n", preview.Index, preview.Description)
		if preview.Diff != "" {
			fmt.Println(preview.Diff)
		}
	}
}

func confirmPlan() bool {
	fmt.Print("Apply this repair plan? [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

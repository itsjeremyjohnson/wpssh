package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/builtbyrobben/wpssh/internal/scripts"
	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// HealthCmd runs a full site health check via the embedded health-check.sh script.
type HealthCmd struct{}

func (c *HealthCmd) Run(g *Globals) error {
	return runScript(g, scripts.ScriptHealthCheck, nil, func(output string, globals *Globals) error {
		if globals.JSON {
			fmt.Println(output)
			return nil
		}
		return formatHealthOutput(output)
	})
}

// BackupCmd creates a named database backup by streaming the dump to this
// machine. Nothing is written on the server.
type BackupCmd struct {
	Description string `arg:"" optional:"" help:"Backup description" default:"Manual"`
}

func (c *BackupCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}
	path, err := defaultDumpPath(site.Alias, c.Description, time.Now())
	if err != nil {
		return err
	}
	dump, err := exportToLocal(context.Background(), rc, site, path)
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}
	return printDump(rc, "Backup saved:", dump)
}

// StatusCmd shows a quick site status (from cache or live).
type StatusCmd struct{}

func (c *StatusCmd) Run(g *Globals) error {
	return runScript(g, scripts.ScriptHealthCheck, nil, func(output string, globals *Globals) error {
		if globals.JSON {
			fmt.Println(output)
			return nil
		}
		return formatStatusOutput(output)
	})
}

// UpdateAllCmd updates core, all plugins, and all themes, then clears cache.
type UpdateAllCmd struct{}

func (c *UpdateAllCmd) Run(g *Globals) error {
	if !g.Yes {
		fmt.Println("This will update WordPress core, all plugins, and all themes.")
		fmt.Println("Use --yes to confirm.")
		return fmt.Errorf("confirmation required: pass --yes to proceed")
	}

	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()

	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	steps := []struct {
		label string
		cmd   string
	}{
		{"Updating core", wpcli.New("core", "update").Build(site.WPPath)},
		{"Updating plugins", wpcli.New("plugin", "update").BoolFlag("all").Build(site.WPPath)},
		{"Updating themes", wpcli.New("theme", "update").BoolFlag("all").Build(site.WPPath)},
	}

	for _, step := range steps {
		fmt.Printf("%s...\n", step.label)
		result, err := rc.ExecWP(ctx, site, step.cmd)
		if err != nil {
			fmt.Printf("  ERROR: %v\n", err)
			continue
		}
		if result.ExitCode != 0 {
			fmt.Printf("  WARNING (exit %d): %s\n", result.ExitCode, strings.TrimSpace(result.Stderr))
		} else {
			fmt.Printf("  OK (%s)\n", result.Duration.Round(time.Millisecond))
		}
	}

	// Clear cache after updates.
	fmt.Println("Clearing cache...")
	script, err := scripts.GetScript(scripts.ScriptCacheClear)
	if err != nil {
		return err
	}
	bashCmd := fmt.Sprintf("bash -s <<'WPGO_SCRIPT'\n%s\nWPGO_SCRIPT", script)
	result, err := rc.ExecWP(ctx, site, bashCmd)
	if err != nil {
		fmt.Printf("  Cache clear error: %v\n", err)
	} else if result.ExitCode != 0 {
		fmt.Printf("  Cache clear warning (exit %d)\n", result.ExitCode)
	} else {
		fmt.Println("  OK")
	}

	fmt.Println("Update complete.")
	return nil
}

// ClearCacheCmd clears all caches via the embedded cache-clear.sh script.
type ClearCacheCmd struct{}

func (c *ClearCacheCmd) Run(g *Globals) error {
	return runScript(g, scripts.ScriptCacheClear, nil, func(output string, globals *Globals) error {
		if globals.JSON {
			fmt.Println(output)
			return nil
		}
		var result struct {
			Status string `json:"status"`
			Steps  []struct {
				Step   string `json:"step"`
				Status string `json:"status"`
			} `json:"steps"`
		}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			fmt.Println(output)
			return nil
		}
		for _, step := range result.Steps {
			icon := "OK"
			if step.Status == "failed" {
				icon = "FAILED"
			} else if step.Status == "skipped" {
				icon = "SKIPPED"
			}
			fmt.Printf("  %-25s %s\n", step.Step, icon)
		}
		return nil
	})
}

// runScript executes an embedded script on the target site via RunContext
// and passes output to a formatter function.
func runScript(g *Globals, scriptName string, args []string, format func(string, *Globals) error) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()

	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	script, err := scripts.GetScript(scriptName)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Build the command to execute the script via heredoc with optional args.
	var cmd string
	if len(args) > 0 {
		quotedArgs := make([]string, len(args))
		for i, arg := range args {
			quotedArgs[i] = shellQuote(arg)
		}
		cmd = fmt.Sprintf("bash -s -- %s <<'WPGO_SCRIPT'\n%s\nWPGO_SCRIPT", strings.Join(quotedArgs, " "), script)
	} else {
		cmd = fmt.Sprintf("bash -s <<'WPGO_SCRIPT'\n%s\nWPGO_SCRIPT", script)
	}

	result, err := rc.ExecWP(ctx, site, cmd)
	if err != nil {
		return fmt.Errorf("execute script: %w", err)
	}

	if g.Verbose && result.Stderr != "" {
		fmt.Fprintf(os.Stderr, "stderr: %s\n", result.Stderr)
	}

	// Extract JSON from output (wp-cli may prepend warnings).
	output := extractJSON(result.Stdout)

	if result.ExitCode != 0 {
		if output != "" {
			fmt.Println(output)
		}
		return fmt.Errorf("%s exited %d: %s", scriptName, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return format(output, g)
}

// extractJSON finds the first JSON object in the output string.
// wp-cli may output warnings before the JSON.
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

// shellQuote wraps a string in single quotes for shell safety.
func shellQuote(s string) string {
	result := "'"
	for _, c := range s {
		if c == '\'' {
			result += "'\\''"
		} else {
			result += string(c)
		}
	}
	result += "'"
	return result
}

// formatHealthOutput displays health check results as a human-readable table.
func formatHealthOutput(output string) error {
	var h struct {
		CoreVersion   string `json:"core_version"`
		PluginCount   int    `json:"plugin_count"`
		PluginUpdates int    `json:"plugin_updates_available"`
		ThemeCount    int    `json:"theme_count"`
		ActiveTheme   string `json:"active_theme"`
		DBSize        string `json:"db_size"`
		DBTableCount  int    `json:"db_table_count"`
		AdminCount    int    `json:"admin_count"`
		CronStatus    string `json:"cron_status"`
		SiteURL       string `json:"site_url"`
		HomeURL       string `json:"home_url"`
		PHPVersion    string `json:"php_version"`
		DiskUsage     string `json:"disk_usage"`
	}
	if err := json.Unmarshal([]byte(output), &h); err != nil {
		fmt.Println(output)
		return nil
	}

	fmt.Println("=== Site Health Check ===")
	fmt.Printf("  WordPress:     %s\n", h.CoreVersion)
	fmt.Printf("  PHP:           %s\n", h.PHPVersion)
	fmt.Printf("  Site URL:      %s\n", h.SiteURL)
	fmt.Printf("  Home URL:      %s\n", h.HomeURL)
	fmt.Printf("  Plugins:       %d installed, %d updates available\n", h.PluginCount, h.PluginUpdates)
	fmt.Printf("  Themes:        %d installed, active: %s\n", h.ThemeCount, h.ActiveTheme)
	fmt.Printf("  Database:      %s (%d tables)\n", h.DBSize, h.DBTableCount)
	fmt.Printf("  Admin users:   %d\n", h.AdminCount)
	fmt.Printf("  Cron:          %s\n", h.CronStatus)
	fmt.Printf("  Disk usage:    %s\n", h.DiskUsage)

	return nil
}

// formatStatusOutput displays a compact status overview.
func formatStatusOutput(output string) error {
	var h struct {
		CoreVersion   string `json:"core_version"`
		PluginCount   int    `json:"plugin_count"`
		PluginUpdates int    `json:"plugin_updates_available"`
		ThemeCount    int    `json:"theme_count"`
		DBSize        string `json:"db_size"`
		PHPVersion    string `json:"php_version"`
		SiteURL       string `json:"site_url"`
	}
	if err := json.Unmarshal([]byte(output), &h); err != nil {
		fmt.Println(output)
		return nil
	}

	fmt.Printf("WP %s | PHP %s | %d plugins", h.CoreVersion, h.PHPVersion, h.PluginCount)
	if h.PluginUpdates > 0 {
		fmt.Printf(" (%d updates)", h.PluginUpdates)
	}
	fmt.Printf(" | %d themes | DB %s\n", h.ThemeCount, h.DBSize)
	fmt.Printf("%s\n", h.SiteURL)

	return nil
}

package cmd

import (
	"fmt"
	"strings"
)

// HelpCmd provides a guided, topic-based help menu.
type HelpCmd struct {
	Topic string `arg:"" optional:"" help:"Help topic (start, setup, sites, content, users, database, maintenance, shortcuts, safety)"`
}

func (c *HelpCmd) Run(_ *Globals) error {
	topic := strings.ToLower(strings.TrimSpace(c.Topic))

	switch topic {
	case "", "start", "overview", "basics":
		printHelpStart()
	case "setup", "onboarding":
		printHelpSetup()
	case "sites":
		printHelpSites()
	case "content":
		printHelpContent()
	case "users":
		printHelpUsers()
	case "database", "data":
		printHelpDatabase()
	case "maintenance", "ops":
		printHelpMaintenance()
	case "shortcuts":
		printHelpShortcuts()
	case "safety":
		printHelpSafety()
	default:
		return fmt.Errorf("unknown help topic %q (run \"wpgo help\" for available topics)", c.Topic)
	}

	return nil
}

func printHelpStart() {
	fmt.Println("wpgo guided help")
	fmt.Println()
	fmt.Println("Topics:")
	fmt.Println("  wpgo help setup         Interactive onboarding and config file creation")
	fmt.Println("  wpgo help sites         Site registry, groups, and targeting")
	fmt.Println("  wpgo help content       Core/plugin/theme/post/media/comment/menu commands")
	fmt.Println("  wpgo help users         User and role management")
	fmt.Println("  wpgo help database      DB, options, cache, transients, search-replace")
	fmt.Println("  wpgo help maintenance   Cron, rewrite, maintenance mode, config/eval/raw")
	fmt.Println("  wpgo help shortcuts     Daily workflow shortcuts")
	fmt.Println("  wpgo help safety        Batch-mode and destructive command protections")
	fmt.Println()
	fmt.Println("Navigation:")
	fmt.Println("  wpgo --help                 Top-level command groups")
	fmt.Println("  wpgo <group> --help         Commands in one group")
	fmt.Println("  wpgo <group> <cmd> --help   Flags/args for one command")
	fmt.Println()
	fmt.Println("Quick start:")
	fmt.Println("  wpgo setup")
	fmt.Println("  wpgo sites list")
	fmt.Println("  wpgo status --site=my-site")
}

func printHelpSetup() {
	fmt.Println("Setup and onboarding")
	fmt.Println()
	fmt.Println("Run interactive setup:")
	fmt.Println("  wpgo setup")
	fmt.Println()
	fmt.Println("What setup can configure:")
	fmt.Println("  - default site alias")
	fmt.Println("  - default output format (table/json/plain)")
	fmt.Println("  - default rate limits")
	fmt.Println("  - cache TTLs")
	fmt.Println("  - site groups")
	fmt.Println()
	fmt.Println("Non-interactive examples:")
	fmt.Println("  wpgo setup --non-interactive --default-site=prod-a")
	fmt.Println("  wpgo setup --non-interactive --default-format=json")
}

func printHelpSites() {
	fmt.Println("Sites and targeting")
	fmt.Println()
	fmt.Println("Registry:")
	fmt.Println("  wpgo sites list")
	fmt.Println("  wpgo sites show <alias>")
	fmt.Println("  wpgo sites groups")
	fmt.Println()
	fmt.Println("Target selection:")
	fmt.Println("  wpgo status --site=<alias>")
	fmt.Println("  wpgo plugin list --group=<group>")
	fmt.Println("  wpgo plugin update --sites=site-a,site-b -y")
	fmt.Println()
	fmt.Println("Metadata overlays:")
	fmt.Println("  wpgo sites add <alias> --wp-path=/var/www/html --host-type=standard")
	fmt.Println("  wpgo sites remove <alias>")
}

func printHelpContent() {
	fmt.Println("Content and WordPress management")
	fmt.Println()
	fmt.Println("Core:")
	fmt.Println("  wpgo core version")
	fmt.Println("  wpgo core check-update")
	fmt.Println("  wpgo core update -y")
	fmt.Println()
	fmt.Println("Plugins and themes:")
	fmt.Println("  wpgo plugin list")
	fmt.Println("  wpgo plugin update --all -y")
	fmt.Println("  wpgo theme list")
	fmt.Println("  wpgo theme update --all -y")
	fmt.Println()
	fmt.Println("Posts, media, comments, menus:")
	fmt.Println("  wpgo post list")
	fmt.Println("  wpgo media image-size")
	fmt.Println("  wpgo comment list")
	fmt.Println("  wpgo menu list")
}

func printHelpUsers() {
	fmt.Println("Users and roles")
	fmt.Println()
	fmt.Println("Users:")
	fmt.Println("  wpgo user list")
	fmt.Println("  wpgo user create <login> <email> --role=administrator -y")
	fmt.Println("  wpgo user reset-password <user> -y")
	fmt.Println()
	fmt.Println("Roles:")
	fmt.Println("  wpgo role list")
	fmt.Println("  wpgo role create <role> <display-name> -y")
}

func printHelpDatabase() {
	fmt.Println("Database and cache")
	fmt.Println()
	fmt.Println("Database:")
	fmt.Println("  wpgo db size")
	fmt.Println("  wpgo db export                 # this machine: ~/wpgo-backups/<site>/<site>_DB_export_<timestamp>_<random>.sql")
	fmt.Println("  wpgo db export pre-update.sql  # this machine: ./pre-update.sql")
	fmt.Println("  (umask 077 && wpgo db export - > dump.sql) || rm -f dump.sql   # stdout; non-zero exit means partial")
	fmt.Println("  (dumps stream over SSH and are never written on the server; set WPGO_LOCAL_BACKUP_DIR to change the dir)")
	fmt.Println("  wpgo db import backup.sql -y --ack-destructive")
	fmt.Println()
	fmt.Println("Options/cache/transients:")
	fmt.Println("  wpgo option list")
	fmt.Println("  wpgo cache flush -y")
	fmt.Println("  wpgo transient list")
	fmt.Println()
	fmt.Println("Search replace:")
	fmt.Println("  wpgo search-replace old.example.com new.example.com --dry-run")
	fmt.Println("  wpgo search-replace old.example.com new.example.com -y --ack-destructive")
}

func printHelpMaintenance() {
	fmt.Println("Maintenance and advanced operations")
	fmt.Println()
	fmt.Println("Maintenance + cron + rewrite:")
	fmt.Println("  wpgo maintenance status")
	fmt.Println("  wpgo cron event list")
	fmt.Println("  wpgo rewrite list")
	fmt.Println()
	fmt.Println("wp-config:")
	fmt.Println("  wpgo config get WP_DEBUG")
	fmt.Println("  wpgo config set WP_DEBUG false -y")
	fmt.Println()
	fmt.Println("Advanced:")
	fmt.Println("  wpgo eval 'echo site_url();'")
	fmt.Println("  wpgo raw plugin list --status=active")
}

func printHelpShortcuts() {
	fmt.Println("Shortcuts")
	fmt.Println()
	fmt.Println("Daily operations:")
	fmt.Println("  wpgo status")
	fmt.Println("  wpgo health")
	fmt.Println("  wpgo backup \"pre-deploy\"        # this machine: ~/wpgo-backups/<site>/; prints path, size, sha256")
	fmt.Println("  wpgo clear-cache -y")
	fmt.Println("  wpgo update-all -y")
}

func printHelpSafety() {
	fmt.Println("Safety model")
	fmt.Println()
	fmt.Println("Read commands:")
	fmt.Println("  No confirmation required.")
	fmt.Println()
	fmt.Println("Mutating batch commands:")
	fmt.Println("  Require -y in non-interactive mode.")
	fmt.Println("  Example: wpgo plugin update --sites=a,b -y")
	fmt.Println()
	fmt.Println("Destructive batch commands:")
	fmt.Println("  Require both -y and --ack-destructive.")
	fmt.Println("  Example: wpgo db reset --sites=a,b -y --ack-destructive")
}

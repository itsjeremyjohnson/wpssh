package cmd

// CLI is the root command struct parsed by Kong.
type CLI struct {
	Globals

	Help        HelpCmd          `cmd:"" help:"Guided help by topic"`
	Setup       SetupCmd         `cmd:"" help:"Interactive onboarding and config setup"`
	Version     VersionCmd       `cmd:"" help:"Show wpgo version"`
	Sites       SitesCmd         `cmd:"" help:"Manage site registry"`
	Core        CoreCmd          `cmd:"" help:"WordPress core management"`
	Plugin      PluginCmd        `cmd:"" help:"Plugin management"`
	Theme       ThemeCmd         `cmd:"" help:"Theme management"`
	DB          DBCmd            `cmd:"" name:"db" help:"Database operations"`
	User        UserCmd          `cmd:"" help:"User management"`
	Post        PostCmd          `cmd:"" help:"Post management"`
	Option      OptionCmd        `cmd:"" help:"Options management"`
	Cache       CacheCmdGroup    `cmd:"" name:"cache" help:"Object cache management"`
	Transient   TransientCmd     `cmd:"" help:"Transients management"`
	Media       MediaCmd         `cmd:"" help:"Media management"`
	SearchRepl  SearchReplaceCmd `cmd:"" name:"search-replace" help:"Database search-replace"`
	Cron        CronCmd          `cmd:"" help:"WP-Cron management"`
	Rewrite     RewriteCmd       `cmd:"" help:"Rewrite rules management"`
	Comment     CommentCmd       `cmd:"" help:"Comment management"`
	Menu        MenuCmd          `cmd:"" help:"Menu management"`
	Config      ConfigCmdGroup   `cmd:"" name:"config" help:"wp-config.php management"`
	Role        RoleCmd          `cmd:"" help:"Role management"`
	Maintenance MaintenanceCmd   `cmd:"" help:"Maintenance mode management"`
	Eval        EvalCmd          `cmd:"" help:"Execute arbitrary PHP. It can write files on the server; take backups and exports with 'wpgo db export'."`
	Raw         RawCmd           `cmd:"" help:"Pass-through to wp-cli"`

	// Shortcut commands
	Health     HealthCmd     `cmd:"" help:"Full site health check"`
	Backup     BackupCmd     `cmd:"" help:"Database backup"`
	Status     StatusCmd     `cmd:"" name:"status" help:"Quick site status"`
	UpdateAll  UpdateAllCmd  `cmd:"" name:"update-all" help:"Update core + plugins + themes"`
	ClearCache ClearCacheCmd `cmd:"" name:"clear-cache" help:"Full cache clear"`
}

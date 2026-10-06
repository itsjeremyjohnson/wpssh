# wpssh

An HTTPS MCP server for managing WordPress sites over SSH. It exposes a fixed set of WordPress tools through the Model Context Protocol's stateless Streamable HTTP transport. The existing `wpgo` CLI remains available while its broader command set is migrated.

## MCP server

Build `bin/wpssh-mcp` with `make build`, or install it with `make install`. The server reads the service account's `~/.ssh/config`, `~/.ssh/known_hosts`, and optional `~/.config/wpgo/config.json` and `sites.json`. Add each target host's verified SSH host key to `known_hosts` before starting the server. The service account must have the SSH key and network access needed for those sites.

Set `WPSMCP_TOKEN` to a random secret of at least 32 characters outside the repository. The MCP endpoint is `/mcp` and requires `Authorization: Bearer <token>` on every request. Configure the MCP client with the HTTPS URL and token.

For containers, set `WPSMCP_TOKEN_FILE` to a mounted secret file. It takes precedence over `WPSMCP_TOKEN`.

By default the server listens on `127.0.0.1:8080`. Terminate HTTPS on the same VPS with a reverse proxy, forwarding `/mcp` to that listener and preserving the original `Host` header. For example, a Caddy site can use `reverse_proxy 127.0.0.1:8080`. Alternatively, set both `WPSMCP_TLS_CERT_FILE` and `WPSMCP_TLS_KEY_FILE`, plus a public `WPSMCP_LISTEN_ADDR`, to serve HTTPS directly. A non-loopback listener requires direct TLS or the explicit proxy setting below.

For a container connected only to a private reverse-proxy network, set `WPSMCP_BEHIND_PROXY=1` and `WPSMCP_LISTEN_ADDR=0.0.0.0:8080`. Do not publish the container port. The proxy must terminate HTTPS before forwarding requests.

The Linode Compose deployment is in `deploy/linode-compose.yaml`. It serves `https://mcp-google.builtbyrobben.com/wpssh/mcp` through the existing Traefik listener. Its `config` bind mount supplies WordPress paths from `sites.json`; keep that file and the token outside the source tree on the VPS. The Traefik route strips `/wpssh` before passing the request to the server's `/mcp` handler.

The default catalog is read-only: `list_sites`, `list_plugins`, `list_themes`, `list_users`, `list_posts`, `core_version`, and `get_option`. Set `WPSMCP_ALLOW_WRITES=1` to also expose `activate_plugin`, `deactivate_plugin`, `update_plugin`, and `update_option`. Each site-specific call requires a `site` alias. No tool accepts raw shell commands or PHP code. Tool results are fresh WP-CLI output; the MCP server does not use the CLI's SQLite cache.

| Variable | Purpose |
|----------|---------|
| `WPSMCP_TOKEN` | Bearer token of at least 32 characters when no token file is set |
| `WPSMCP_TOKEN_FILE` | Path to a bearer-token secret file; overrides `WPSMCP_TOKEN` |
| `WPSMCP_LISTEN_ADDR` | Listener address, default `127.0.0.1:8080` |
| `WPSMCP_TLS_CERT_FILE`, `WPSMCP_TLS_KEY_FILE` | Optional pair for direct HTTPS |
| `WPSMCP_ALLOW_WRITES` | Set to `1` to expose the four write tools |
| `WPSMCP_BEHIND_PROXY` | Permit plain HTTP on a non-loopback listener behind an HTTPS reverse proxy |

`wpssh-mcp --version` prints the build version without starting the server.

## Legacy CLI

The legacy `wpgo` command keeps its existing SSH host-key behavior. The MCP server requires pinned keys in `known_hosts`.

### Installation

### Homebrew (macOS/Linux)

```bash
brew tap builtbyrobben/tap
brew install wpssh
```

### Download Binary

Download the latest release from [GitHub Releases](https://github.com/builtbyrobben/wpssh/releases).

### Build from Source

```bash
git clone https://github.com/builtbyrobben/wpssh.git
cd wpssh
make build-cli
```

## Configuration

wpgo discovers sites from your SSH config (`~/.ssh/config`) and enriches them with optional metadata overlays. Run the interactive setup to get started.

```bash
wpgo setup
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `WPGO_SITE` | Default target site alias |
| `WPGO_LOCAL_BACKUP_DIR` | Local dir for `db export` and `backup` dumps (default `~/wpgo-backups`) |

### Site Registry

Sites are auto-discovered from SSH config. You can add metadata overlays to enrich them:

```bash
# List all sites
wpgo sites list

# Show site details
wpgo sites show mysite

# Add metadata (WordPress path, host type, groups)
wpgo sites add mysite --wp-path /var/www/html --host-type wpengine --add-group production

# Remove metadata overlay
wpgo sites remove mysite

# List site groups
wpgo sites groups

# Test SSH connectivity
wpgo sites test mysite
wpgo sites test --all
```

## Commands

### Targeting Sites

Most commands require a target site:

```bash
# Target a single site
wpgo -s mysite plugin list

# Target multiple sites (batch mode)
wpgo --sites mysite1,mysite2 plugin list

# Target a site group
wpgo -g production plugin list

# Use WPGO_SITE env var as default
export WPGO_SITE=mysite
wpgo plugin list
```

### sites -- Site registry management

```bash
wpgo sites list                         # List all registered sites
wpgo sites show <alias>                 # Show site details
wpgo sites groups                       # List configured groups
wpgo sites add <alias> [--wp-path ...] [--host-type ...] [--add-group ...]
wpgo sites remove <alias>               # Remove metadata overlay
wpgo sites test <alias>                 # Test SSH connectivity
wpgo sites test --all                   # Test all sites
```

### plugin -- Plugin management

```bash
wpgo -s mysite plugin list                   # List all plugins
wpgo -s mysite plugin list --status active   # Filter by status
wpgo -s mysite plugin install woocommerce    # Install a plugin
wpgo -s mysite plugin install woocommerce --activate  # Install and activate
wpgo -s mysite plugin activate woocommerce   # Activate a plugin
wpgo -s mysite plugin deactivate woocommerce # Deactivate a plugin
wpgo -s mysite plugin delete woocommerce     # Delete a plugin
wpgo -s mysite plugin update woocommerce     # Update a plugin
wpgo -s mysite plugin update --all           # Update all plugins
wpgo -s mysite plugin search "seo"           # Search WordPress.org
wpgo -s mysite plugin get woocommerce        # Get plugin details
wpgo -s mysite plugin is-active woocommerce  # Check if active
wpgo -s mysite plugin is-installed woocommerce  # Check if installed
wpgo -s mysite plugin status                 # Show plugin status
wpgo -s mysite plugin verify-checksums       # Verify all plugin checksums
wpgo -s mysite plugin auto-updates enable woocommerce   # Enable auto-updates
wpgo -s mysite plugin auto-updates disable woocommerce  # Disable auto-updates
wpgo -s mysite plugin auto-updates status    # Show auto-update status
```

### theme -- Theme management

```bash
wpgo -s mysite theme list
wpgo -s mysite theme install flavor
wpgo -s mysite theme activate flavor
wpgo -s mysite theme delete flavor
wpgo -s mysite theme update --all
```

### core -- WordPress core management

```bash
wpgo -s mysite core version
wpgo -s mysite core update
wpgo -s mysite core verify-checksums
```

### db -- Database operations

```bash
wpgo -s mysite db export                 # this machine: ~/wpgo-backups/mysite/mysite_DB_export_<timestamp>_<random>.sql
wpgo -s mysite db export pre-update.sql  # this machine: ./pre-update.sql
(umask 077 && wpgo -s mysite db export - > dump.sql)  # stdout; see below
wpgo -s mysite db import dump.sql
wpgo -s mysite db query "SELECT COUNT(*) FROM wp_posts"
wpgo -s mysite db size
wpgo -s mysite db tables
wpgo -s mysite db optimize
wpgo -s mysite db repair
```

`db export` and `backup` run `wp db export -` on the server and stream the dump over SSH to the machine running wpgo. Nothing is written on the server, and wpgo has no option to write one there. The default destination is `${WPGO_LOCAL_BACKUP_DIR:-~/wpgo-backups}/<site>/<site>_DB_<desc>_<timestamp>_<ms>_<random>.sql` (dir 0700, file 0600). wpgo writes to a temp file, fsyncs it and links it into place, then prints the path, byte size and sha256. It never replaces an existing file, including one created while the dump was streaming. If the remote export exits non-zero, the dump is empty, or its last non-empty line does not start with mysqldump's `-- Dump completed`, wpgo deletes the partial file and exits non-zero.

`db export -` applies the same checks and exits non-zero on failure, but it cannot take back bytes already written to stdout, so check the exit status before using the output. Redirect it under `umask 077` so the dump is not world-readable:

```bash
(umask 077 && wpgo -s mysite db export - > dump.sql) || rm -f dump.sql
```

### user -- User management

```bash
wpgo -s mysite user list
wpgo -s mysite user get admin
wpgo -s mysite user create --email new@example.com --role editor
```

### post -- Post management

```bash
wpgo -s mysite post list
wpgo -s mysite post get 42
wpgo -s mysite post delete 42
```

### option -- Options management

```bash
wpgo -s mysite option get siteurl
wpgo -s mysite option update blogdescription "My Site"
```

### search-replace -- Database search and replace

```bash
wpgo -s mysite search-replace "http://old.example.com" "https://new.example.com"
```

### cache -- Object cache management

```bash
wpgo -s mysite cache flush
wpgo -s mysite cache type
```

### transient -- Transients management

```bash
wpgo -s mysite transient delete --all
wpgo -s mysite transient get my_transient
```

### cron -- WP-Cron management

```bash
wpgo -s mysite cron event list
wpgo -s mysite cron event run
```

### rewrite -- Rewrite rules management

```bash
wpgo -s mysite rewrite flush
wpgo -s mysite rewrite list
```

### comment -- Comment management

```bash
wpgo -s mysite comment list
wpgo -s mysite comment approve 15
wpgo -s mysite comment delete 15
```

### menu -- Menu management

```bash
wpgo -s mysite menu list
```

### config -- wp-config.php management

```bash
wpgo -s mysite config get DB_NAME
wpgo -s mysite config set WP_DEBUG true
```

### role -- Role management

```bash
wpgo -s mysite role list
```

### maintenance -- Maintenance mode

```bash
wpgo -s mysite maintenance enable
wpgo -s mysite maintenance disable
```

### eval -- Execute arbitrary PHP

```bash
wpgo -s mysite eval "echo get_option('siteurl');"
```

`eval` runs any PHP, so it can write files on the server. Take backups and exports with `wpgo db export`, never with `eval`.

### raw -- Pass-through to wp-cli

```bash
wpgo -s mysite raw -- option list
wpgo -s mysite raw -- post meta update 7 title "'Spring cleaning tips'"
```

Put wp-cli arguments after `--`. `raw` joins them with spaces and the server shell parses that line, so quote any argument that holds spaces or shell characters, in single quotes. Before connecting, `raw` refuses:

- shell syntax outside single quotes: `;`, `&`, `|`, `<`, `>`, a newline, `(`, `)`, `{`, `}`, a leading `#`, `$` or a backtick (also inside double quotes), a line continuation, an unbalanced quote, the glob characters `*`, `?` and `[`, or a `~` at the start of a word or after `=` or `:`. Quote or backslash-escape them to pass them literally;
- `db export` (or `db dump`) with a file argument other than `-`, with no file argument, with a flag outside a short mysqldump allowlist (blocks `--result-file`, `--tab`, their prefixes and `--defaults`), or with a `--tables` or `--exclude_tables` entry that starts with `-`. Use `wpgo db export`;
- `export` (the WXR exporter);
- `search-replace` or `db search-replace` with any `--export` flag;
- `db query` whose SQL contains `OUTFILE` or `DUMPFILE` in any case, a mysql client command that runs or writes files (`system`, `tee`, `pager`, `source` or `edit` at the start of a statement, or `\!`, `\T`, `\P`, `\.` or `\e` anywhere, in any case), or the same in an `--execute` value;
- `db query`, `import`, `create`, `drop`, `reset`, `clean`, `check`, `optimize`, `repair`, `tables`, `size`, `columns` and `prefix` with a flag outside a short per-command allowlist. wp-cli hands extra flags to mysql or mysqlcheck, so this blocks `--tee`, `--pager`, `--init-command`, `--execute` (except on `db query`, where its SQL is checked), `--defaults` and `--defaults-*` (`--no-defaults` passes), and their prefixes. `wpgo db query` and `wpgo db import` apply the same checks;
- `db cli` and `db connect`;
- the `--exec` and `--require` globals.

`sql` counts as `db`, and any run of `--` delimiters is skipped. On WP Engine, whose gateway parses each word a second time, `raw` checks that second layer too.

`eval` and `eval-file` are the one exception: `raw` checks only their shell syntax, never their PHP, and allows `--exec` and `--require` with them. PHP can write files on the server, so take backups and exports with `wpgo db export`, never with `eval`.

When stdin is piped or redirected from a file, `raw` forwards it to wp, so wp-cli's `-` file argument works:

```bash
wpgo -s mysite raw -- post update 7 - < content.html
```

`raw` refuses a `-` argument, before connecting, when stdin is a terminal, `/dev/null` or empty: wp would read nothing and write empty content. `db export -` and `db dump -` are exempt, since there `-` means stdout.

These checks stop agents and operators from writing dumps on the server by accident or in passing. `eval` and `eval-file` are the deliberate escape hatch. The checks are not a sandbox against an operator who sets out to get around them.

### Shortcut Commands

```bash
wpgo -s mysite health         # Full site health check
wpgo -s mysite status         # Quick site status overview
wpgo -s mysite backup         # Database backup streamed to ~/wpgo-backups/mysite/ on this machine
wpgo -s mysite backup "Pre-update snapshot"  # Backup with description
wpgo -s mysite update-all -y  # Update core + plugins + themes
wpgo -s mysite clear-cache    # Full cache clear
```

### setup and help

```bash
wpgo setup                    # Interactive onboarding
wpgo help                     # Guided help by topic
wpgo version                  # Show version
```

## Global Flags

| Flag | Description |
|------|-------------|
| `-s`, `--site` | Target site alias |
| `--sites` | Multiple target sites (comma-separated, batch mode) |
| `-g`, `--group` | Target site group |
| `--json` | Output as JSON |
| `--plain` | Output as plain text |
| `-v`, `--verbose` | Verbose output |
| `--dry-run` | Show commands without executing |
| `--no-cache` | Bypass cache |
| `--fields` | Comma-separated fields to display |
| `-y`, `--yes` | Skip confirmation prompts |
| `--ack-destructive` | Acknowledge destructive batch operations |
| `--concurrency` | Max parallel executions in batch mode (default: 1) |

## License

MIT

# Repository Guidelines

## Project Structure

- `cmd/wpssh-mcp/`: HTTPS MCP server entrypoint
- `cmd/wpgo/`: legacy CLI entrypoint
- `internal/`: implementation packages
  - `cmd/`: Kong CLI commands
  - `ssh/`: SSH client
  - `cache/`: Caching
  - `wpcli/`: WP-CLI wrapper
  - `config/`: Configuration
  - `outfmt/`: JSON/plain output formatting
  - `errfmt/`: User-friendly error formatting
  - `safety/`: Safety checks
  - `scripts/`: Script management
  - `adapter/`: Adapters
  - `batch/`: Batch operations
  - `registry/`: Registry
  - `mcpserver/`: MCP tools and HTTP authentication

## Build, Test, and Development Commands

- `make` / `make build`: build `bin/wpssh-mcp`
- `make build-cli`: build legacy `bin/wpgo`
- `make tools`: install pinned dev tools into `.tools/`
- `make fmt` / `make lint` / `make test` / `make ci`: format, lint, test, full local gate
- `make test-integration`: run integration tests
- `make test-all`: run all tests including integration

## Coding Style & Naming Conventions

- Formatting: `make fmt` (`goimports` local prefix `github.com/builtbyrobben/wpssh` + `gofumpt`)
- Output: keep stdout parseable (`--json` / `--plain`); send human hints/progress to stderr

## Testing Guidelines

- Unit tests: stdlib `testing` (and `httptest` where needed)
- Integration tests: use `//go:build integration` tag under `tests/integration/`

## Commit & Pull Request Guidelines

- Follow Conventional Commits + action-oriented subjects (e.g. `feat(cli): add --verbose flag`)
- Group related changes; avoid bundling unrelated refactors
- PRs should summarize scope, note testing performed, and mention user-facing changes

## Security

- Never commit SSH keys, passwords, or credentials
- Use `--stdin` for sensitive input (avoid shell history leaks)
- `internal/cmd/dumpscan.go` must read a dump exactly as the mysql and mariadb clients do (`add_line` and `read_and_execute` in `client/mysql.cc` of mysql-server and MariaDB/server). Check each lexer rule against both sources, not memory. Where the clients or the server disagree, refuse the construct. For every bypass, add a rejection case to `TestScanDumpRefusesClientCommands`, which runs whole-input and one-byte reads.

# Repository Guidelines

## Project Status & Structure

FastCI is a Go CLI (module `github.com/Baiyuani/FastCI`, binary `fastci`) that
mirrors a local git Working copy into a GitHub repository, injects a GitHub
Actions workflow, and has that repository build and push the Working copy's
Dockerfile as a container image. See `README.md` for usage, `GLOSSARY.md` for
the project's vocabulary, and `docs/adr/` for decisions that should not be
re-litigated by accident.

The `/.agents`, `/.aws`, and `/.codex` directories are empty environment
scaffolding, not project code. Do not treat them as source or build inputs.

- `cmd/fastci/` — `main`, which only calls `cli.Execute`
- `internal/cli/` — cobra commands and how they print their results
- `internal/config/` — global config, `fastci.json`, the State file, the Marker
- `internal/github/` — the slice of the GitHub REST API FastCI calls
- `internal/sync/` — the Sync itself: what travels, staging, git, push
- `internal/workflow/` — the workflow template baked into the binary
- `docs/` — ADRs and design notes

## Build, Test, and Development Commands

```bash
go build ./...                              # compile everything
go test ./...                               # run the whole suite; needs no network
gofmt -l .                                  # must print nothing
go vet ./...                                # must print nothing
go build -ldflags "-X github.com/Baiyuani/FastCI/internal/cli.Version=$(git describe --tags --always)" -o fastci ./cmd/fastci
```

Tests bind a localhost port (an `httptest` server stands in for GitHub) and run
`git` against temporary repositories. They never touch github.com and never need
a credential, so they also run with no network access.

Never commit the built binary or anything else generated (`.fastci.json` files
produced by a local run, state files, caches).

## Coding Style & Naming Conventions

- Run `gofmt`; commit only formatted code. Tabs for Go indentation.
- Lowercase, underscore-free file names (`workspace.go`, not `workspace_utils.go`);
  `PascalCase` for exported types, `camelCase` for the rest.
- Two-space indentation in YAML and JSON.
- Comments say why, not what. A comment that only restates the code should go.
- User-facing strings are lowercase and name the file or flag the user should
  look at, the way git's own messages do.
- Keep the `LICENSE` header intact; do not add per-file copyright banners.

## Testing Guidelines

Tests sit next to the code as `<module>_test.go`; `go test ./...` is the one
command. Cover new branches, parsers, and anything touching credentials or
deletion. A test that talks to the real GitHub API must be behind a
`//go:build integration` tag and must not run by default.

## Commit & Pull Request Guidelines

History contains one commit (`Initial commit`), so no house style is established
yet. Use imperative, capitalized subjects under 72 characters, scoped when
useful (`Add:`, `Fix:`, `Docs:`).

Pull requests should include a short summary, the motivation, the commands run
to verify, and a linked issue when one exists. Add screenshots for UI changes.

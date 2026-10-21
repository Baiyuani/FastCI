# FastCI

[English](README.md) | [简体中文](README.zh-CN.md)

FastCI takes a local git repository (a **Workspace**), copies it into a new
GitHub repository (**Target repo**), adds a GitHub Actions workflow, and lets
that workflow build the Workspace's `Dockerfile` and push the image to a
container registry.

You keep working in your Workspace. FastCI handles the repo, the workflow, the
build, and the tags.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/Baiyuani/FastCI/main/install.sh | sh
```

`VERSION=v1.2.3` installs a specific release, `INSTALL_DIR=...` changes where it
lands. Windows users can download the `.zip` from
[Releases](https://github.com/Baiyuani/FastCI/releases). Developing FastCI
itself: `go build -o fastci ./cmd/fastci`.

## Requirements

- `git` on your `PATH`.
- A GitHub personal access token with the `repo` and `workflow` scopes. The
  `workflow` scope is what lets FastCI add a file under `.github/workflows/`;
  without it the push is rejected.
- `docker` is **not** needed locally. Images are built by GitHub Actions.

```sh
export GITHUB_TOKEN=ghp_...        # or put "token" in the config file below
```

## Quick start

```sh
cd ~/src/my-app
fastci push
```

That creates the private repo `<your-account>/my-app`, injects
`.github/workflows/fastci-build.yml`, and pushes one commit. The push triggers
the build, which publishes `ghcr.io/<your-account>/my-app`.

Then, as you work:

```sh
fastci update          # copy the Workspace over the Target repo as one commit
fastci status          # last sync and the last CI run
fastci open --run      # open the newest workflow run in the browser
```

## Commands

| Command | What it does |
| --- | --- |
| `fastci push [workspace]` | Create the Target repo and sync the Workspace into it |
| `fastci update [workspace\|owner/repo]` | Sync the Workspace again, one commit |
| `fastci list` | Every Target repo this machine created (`--json`) |
| `fastci status [workspace\|owner/repo]` | One Target repo, its last sync, its last run (`--json`) |
| `fastci open [workspace\|owner/repo]` | Open the repo (`--actions`, `--run`) in the browser |
| `fastci delete [workspace\|owner/repo]` | Delete the repo on GitHub and forget it (`--yes`) |

Every command takes a Workspace directory or an `owner/repo` FastCI knows about,
and defaults to the current directory. Exit codes are `0` for success and `1`
for a failure; `2` means the command line itself was wrong.

`push` and `update` accept `--owner`, `--repo`, `--image`, `--dockerfile`,
`--tags`, `--workflow`, `--public`, `--registry-username`, and
`--registry-password`. `update` also accepts `--force`.

## Configuration

Four sources, each one overriding the one before it:

1. The global config, `~/.config/fastci/config.json` (or `--config <path>`):

   ```json
   {
     "token": "ghp_...",
     "owner": "my-account",
     "registry_username": "robot",
     "registry_password": "hunter2"
   }
   ```

   `GITHUB_TOKEN` overrides `token`.

2. The record of the last Sync, in `~/.config/fastci/state.json`. FastCI writes
   this, so `fastci update` does not need every flag repeated.
3. `fastci.json` in the Workspace, which FastCI copies into the Target repo. It
   accepts `owner`, `repo`, `image`, `dockerfile`, `tags`, `workflow`, and
   `public`. It travels into the repository, so **credentials go in the global
   config, never here**: unknown keys are rejected.
4. The command-line flags.

FastCI never writes to your Workspace.

## What a Sync copies

Everything `git ls-files -co --exclude-standard` reports, so committed files and
uncommitted ones that are not ignored. On top of that:

- The `Dockerfile` and `fastci.json` always travel, even when the Workspace
  ignores them.
- `.git`, nested repositories, submodules, and the `.codegraph`, `.agents`,
  `.codex`, and `.aws` directories never travel. A nested repository is skipped
  with a warning rather than pushed as a broken submodule.
- Symlinks stay symlinks.
- Files that look like credentials (`id_rsa*`, `*.pem`, a root `.env`) are
  listed as warnings. They still travel: that is your call, not FastCI's.
- A file over 90 MB or a Workspace over 900 MB stops the Sync.

The `.gitignore` in the Target repo does not hide anything from the push, so a
Workspace that ignores its own `Dockerfile` still gets one.

## Updating, and what FastCI owns

A Target repo is a **mirror**: every Sync makes it match the Workspace exactly.
Deleting a file in the Workspace deletes it in the Target repo, and FastCI
rewrites `.fastci.json` and the workflow from the current tool version.

FastCI never merges. If the Target repo changed outside FastCI (a hand-edit in
the GitHub UI, a merge from someone else), `update` stops and tells you to look
at it; `--force` overwrites it. The Target repo's history is generated: one
commit per Sync, `sync: <workspace> @ <commit>`. Your Workspace's own commits
are never pushed to it.

## Image tags

Every Sync renders its tags into the workflow. The default is `latest`, plus
`sha-<commit>` where `<commit>` is the Workspace's HEAD commit, not the Sync
commit. Configured tags **replace** `latest`; the `sha-` tag is always added,
because it is the tag you can look up from the Workspace:

```sh
fastci push --tags v1,stable
# -> ghcr.io/me/app:v1, ghcr.io/me/app:stable, ghcr.io/me/app:sha-1a2b3c4
```

`sha-*` names the commit FastCI saw, so a Sync from a Workspace with
uncommitted changes publishes an image that names only the base commit.

## Registries

The default is `ghcr.io`, and the build logs in with the run's own
`GITHUB_TOKEN`, so nothing needs configuring. For another registry, name it in
the image and hand FastCI the credentials:

```sh
fastci push --image registry.example.com/team/app \
  --registry-username robot --registry-password hunter2
```

Those become the `REGISTRY_USERNAME` and `REGISTRY_PASSWORD` Actions secrets
(replacing any previous values) and are never written into the workflow file.
The last `--tags`/image settings are remembered for `update`.

The workflow builds `linux/amd64` only. Add platforms in
`.github/workflows/fastci-build.yml` if you need more, and note that a later
`update` will overwrite that edit.

## Pulling a private image

Target repos are private by default, and the first publish to GHCR inherits
that, so the image is private too. Pulling it needs a login:

```sh
echo "$GITHUB_TOKEN" | docker login ghcr.io -u <your-account> --password-stdin
docker pull ghcr.io/<your-account>/my-app:latest
```

The package stays in GitHub Packages after `fastci delete`; FastCI never
deletes images, so delete them by hand in the package settings if you want them
gone.

## Development

```sh
go test ./...   # no network, no credentials: a fake GitHub API and temp repos
go vet ./...
gofmt -l .
```

`GLOSSARY.md` defines the project's vocabulary; `docs/adr/` records the
decisions behind the behavior above.

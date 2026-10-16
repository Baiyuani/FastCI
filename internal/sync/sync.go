// Package sync makes a Target repo's contents match a Workspace and pushes the
// result as a single commit.
package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Baiyuani/FastCI/internal/config"
	"github.com/Baiyuani/FastCI/internal/github"
	"github.com/Baiyuani/FastCI/internal/workflow"
)

// DefaultBranch is the only branch FastCI ever writes. ADR-0001: a Target repo
// carries generated history, never the Workspace's.
const DefaultBranch = "main"

// Mode says whether a Sync creates the Target repo or updates one.
type Mode int

const (
	// ModePush creates the Target repo and pushes its first commit.
	ModePush Mode = iota
	// ModeUpdate pushes a new commit onto an existing Target repo.
	ModeUpdate
)

// Options is one Sync.
type Options struct {
	Settings config.Settings
	Mode     Mode
	Force    bool
	Version  string
}

// Result reports what a Sync did.
type Result struct {
	RepoURL   string
	Commit    string
	Changed   bool
	ImageRefs []string
	Warnings  []string
}

// ImageRefs is the tag set a Sync publishes: the configured tags, or latest
// when none are configured, plus the immutable tag naming the Workspace commit.
// The sha is the Workspace's, not the Sync commit's, so `git log` in the
// Workspace finds it (ADR-0004).
func ImageRefs(image string, tags []string, workspaceCommit string) []string {
	if len(tags) == 0 {
		tags = []string{"latest"}
	}
	refs := make([]string, 0, len(tags)+1)
	for _, tag := range tags {
		refs = append(refs, image+":"+tag)
	}
	return append(refs, image+":sha-"+workspaceCommit)
}

// Run performs one Sync. Every check that can fail without touching GitHub runs
// first; creating the Target repo is the only irreversible step, and it happens
// once the commit is ready to push.
func Run(gh *github.Client, state *config.State, opts Options) (Result, error) {
	ws, err := openWorkspace(opts.Settings.Workspace, opts.Settings.Dockerfile)
	if err != nil {
		return Result{}, err
	}

	key := opts.Settings.Key()
	owner, name := opts.Settings.Owner, opts.Settings.Repo
	recorded, hasRecord := state.Get(key)

	var remote github.Repo
	exists := false
	switch opts.Mode {
	case ModePush:
		if hasRecord {
			return Result{}, fmt.Errorf("%s is already recorded in %s; run `fastci update` instead", key, state.Path())
		}
		if remote, exists, err = gh.GetRepo(owner, name); err != nil {
			return Result{}, err
		}
		if exists {
			commits, err := gh.HasCommits(owner, name)
			if err != nil {
				return Result{}, err
			}
			if commits {
				return Result{}, fmt.Errorf("%s already exists on GitHub; run `fastci update` instead", key)
			}
			// An empty repo is what a previous run that failed between create and
			// push leaves behind, so re-running push must adopt it.
		}
	case ModeUpdate:
		if !hasRecord {
			return Result{}, fmt.Errorf("%s is not in %s; run `fastci push` first", key, state.Path())
		}
		if remote, exists, err = gh.GetRepo(owner, name); err != nil {
			return Result{}, err
		}
		if !exists {
			return Result{}, fmt.Errorf("Target repo %s no longer exists on GitHub", key)
		}
	}

	dir, err := os.MkdirTemp("", "fastci-stage-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	if _, err := git(dir, "init", "--quiet"); err != nil {
		return Result{}, err
	}
	if _, err := git(dir, "symbolic-ref", "HEAD", "refs/heads/"+DefaultBranch); err != nil {
		return Result{}, err
	}

	if opts.Mode == ModeUpdate {
		base, err := fetchBranch(dir, remote.CloneURL, gh.Token, DefaultBranch)
		if err != nil {
			return Result{}, fmt.Errorf("reading %s: %w", remote.HTMLURL, err)
		}
		if base != recorded.LastSyncSHA && !opts.Force {
			return Result{}, fmt.Errorf("%s changed outside FastCI (HEAD %s, last sync %s); look at it, then re-run with --force to overwrite",
				key, shortSHA(base), shortSHA(recorded.LastSyncSHA))
		}
		if _, err := git(dir, "update-ref", "refs/heads/"+DefaultBranch, base); err != nil {
			return Result{}, err
		}
	}

	result := Result{Warnings: ws.warnings}
	result.ImageRefs = ImageRefs(opts.Settings.Image, opts.Settings.Tags, ws.commit)
	if !opts.Settings.UsesGitHubToken() && opts.Settings.RegistryPassword == "" && opts.Settings.RegistryUsername == "" {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%s is not ghcr.io and no credentials were given: the build will reference secrets.REGISTRY_USERNAME and secrets.REGISTRY_PASSWORD, which nobody has set yet",
			opts.Settings.Host()))
	}

	rendered, err := workflow.Render(workflow.Params{
		Host:       opts.Settings.Host(),
		Image:      opts.Settings.Image,
		Dockerfile: opts.Settings.Dockerfile,
		Tags:       result.ImageRefs,
		Username:   loginUser(opts.Settings, "${{ secrets.REGISTRY_USERNAME }}"),
		Password:   loginPassword(opts.Settings, "${{ secrets.REGISTRY_PASSWORD }}"),
	})
	if err != nil {
		return Result{}, err
	}

	fingerprint, err := copyTree(ws, dir)
	if err != nil {
		return Result{}, err
	}
	marker, err := json.MarshalIndent(config.Marker{
		ToolVersion:     opts.Version,
		TemplateVersion: workflow.TemplateVersion,
		WorkspaceCommit: ws.commit,
		WorkspaceOrigin: ws.origin,
		Fingerprint:     fingerprint,
		Image:           opts.Settings.Image,
		Dockerfile:      opts.Settings.Dockerfile,
	}, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if err := writeFile(dir, workflow.Path(opts.Settings.Workflow), rendered); err != nil {
		return Result{}, err
	}
	if err := writeFile(dir, config.MarkerFile, string(marker)+"\n"); err != nil {
		return Result{}, err
	}
	// -f: the Workspace's .gitignore (and the user's global excludes) travel
	// with the files, and must not drop anything out of the Target repo.
	if _, err := git(dir, "add", "-A", "-f"); err != nil {
		return Result{}, err
	}

	if opts.Mode == ModeUpdate {
		same, err := indexMatchesHead(dir)
		if err != nil {
			return Result{}, err
		}
		if same {
			result.RepoURL = remote.HTMLURL
			if err := writeSecrets(gh, opts.Settings, &result); err != nil {
				return Result{}, err
			}
			return result, nil
		}
	}

	message := fmt.Sprintf("sync: %s @ %s", filepath.Base(ws.path), ws.commit)
	if _, err := git(dir, "-c", "user.name=fastci", "-c", "user.email=fastci@users.noreply.github.com",
		"commit", "--quiet", "-m", message); err != nil {
		return Result{}, err
	}
	commit, err := gitTrim(dir, "rev-parse", "HEAD")
	if err != nil {
		return Result{}, err
	}

	if !exists {
		if remote, err = gh.CreateRepo(owner, name, !opts.Settings.Public); err != nil {
			return Result{}, fmt.Errorf("creating %s: %w", key, err)
		}
	}
	if err := pushBranch(dir, remote.CloneURL, gh.Token, DefaultBranch); err != nil {
		return Result{}, fmt.Errorf("pushing to %s (%s): %w", key, remote.HTMLURL, err)
	}
	if !exists {
		// The first push decides the branch, but do not leave that to chance.
		if err := gh.SetDefaultBranch(owner, name, DefaultBranch); err != nil {
			result.Warnings = append(result.Warnings, "could not set the default branch: "+err.Error())
		}
	}
	if err := writeSecrets(gh, opts.Settings, &result); err != nil {
		return Result{}, err
	}

	state.Put(key, config.Entry{
		Owner: owner, Repo: name, Workspace: ws.path,
		Image: opts.Settings.Image, Dockerfile: opts.Settings.Dockerfile,
		Tags: opts.Settings.Tags, Workflow: opts.Settings.Workflow,
		LastSyncSHA: commit, LastSyncAt: time.Now().UTC().Format(time.RFC3339),
		WorkspaceSHA: ws.commit,
	})
	if err := state.Save(); err != nil {
		return Result{}, err
	}

	result.RepoURL, result.Commit, result.Changed = remote.HTMLURL, commit, true
	return result, nil
}

func loginUser(s config.Settings, fallback string) string {
	if s.UsesGitHubToken() {
		return "${{ github.actor }}"
	}
	return fallback
}

func loginPassword(s config.Settings, fallback string) string {
	if s.UsesGitHubToken() {
		return "${{ secrets.GITHUB_TOKEN }}"
	}
	return fallback
}

// writeSecrets stores registry credentials as Actions secrets, replacing
// whatever was there. ghcr.io with no credentials needs none: the workflow logs
// in with the run's GITHUB_TOKEN.
func writeSecrets(gh *github.Client, s config.Settings, result *Result) error {
	if s.RegistryUsername == "" && s.RegistryPassword == "" {
		return nil
	}
	for _, secret := range []struct{ name, value string }{
		{"REGISTRY_USERNAME", s.RegistryUsername},
		{"REGISTRY_PASSWORD", s.RegistryPassword},
	} {
		if err := gh.PutSecret(s.Owner, s.Repo, secret.name, secret.value); err != nil {
			return fmt.Errorf("storing the %s secret: %w", secret.name, err)
		}
	}
	result.Warnings = append(result.Warnings, "wrote REGISTRY_USERNAME and REGISTRY_PASSWORD; any previous values are replaced")
	return nil
}

// copyTree copies the Workspace's files into the staging repo and returns a
// fingerprint of exactly what was copied.
func copyTree(ws *workspace, dir string) (string, error) {
	digest := sha256.New()
	for _, f := range ws.files {
		src := filepath.Join(ws.path, filepath.FromSlash(f.Path))
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if f.Mode&os.ModeSymlink != 0 {
			target, err := os.Readlink(src)
			if err != nil {
				return "", err
			}
			if err := os.Symlink(target, dst); err != nil {
				return "", err
			}
			fmt.Fprintf(digest, "%s\x00link:%s\n", f.Path, target)
			continue
		}
		sum, err := copyFile(src, dst, f.Mode)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(digest, "%s\x00%s\n", f.Path, sum)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func copyFile(src, dst string, mode fs.FileMode) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	perm := fs.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return "", err
	}
	defer out.Close()
	sum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, sum), in); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func writeFile(dir, path, content string) error {
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

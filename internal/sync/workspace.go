package sync

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/Baiyuani/FastCI/internal/config"
)

const (
	// GitHub rejects files over 100 MB outright, and the repo limit is not far
	// behind. Refuse well before either.
	maxFileSize  = 90 << 20
	maxTotalSize = 900 << 20
)

// excludedTopLevel is what FastCI always drops. A Workspace's .gitignore does
// not mention environment scaffolding, and it has no business traveling.
var excludedTopLevel = map[string]bool{
	".git": true, ".codegraph": true, ".agents": true, ".codex": true, ".aws": true,
}

// File is one thing a Sync copies.
type File struct {
	Path string // slash-separated, relative to the Workspace
	Size int64
	Mode fs.FileMode
}

// workspace is a Workspace prepared for a Sync.
type workspace struct {
	path     string
	commit   string // Workspace HEAD, short
	origin   string
	files    []File
	warnings []string
}

// openWorkspace validates a Workspace and lists what a Sync would copy. It
// writes nothing, so every check that can fail happens before any remote
// mutation.
func openWorkspace(dir, dockerfile string) (*workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}
	if _, err := git(abs, "rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("%s is not a git repository", abs)
	}
	commit, err := gitTrim(abs, "rev-parse", "--short=7", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("%s has no commits yet: commit something first", abs)
	}
	if err := checkDockerfile(abs, dockerfile); err != nil {
		return nil, err
	}
	files, warnings, err := ListFiles(abs, dockerfile)
	if err != nil {
		return nil, err
	}
	origin, _ := gitTrim(abs, "config", "--get", "remote.origin.url")
	return &workspace{path: abs, commit: commit, origin: origin, files: files, warnings: warnings}, nil
}

// checkDockerfile rejects a missing file, or one pointing outside the Workspace.
func checkDockerfile(dir, rel string) error {
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || strings.HasPrefix(clean, "../") || filepath.IsAbs(rel) {
		return fmt.Errorf("--dockerfile %s must be a path inside the Workspace", rel)
	}
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(clean)))
	if err != nil {
		return fmt.Errorf("Dockerfile %s: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Dockerfile %s is not a file", rel)
	}
	return nil
}

// ListFiles returns what a Sync copies: everything git calls tracked or
// untracked-but-not-ignored, minus FastCI's own exclusions, plus the Dockerfile
// and fastci.json even when the Workspace ignores them.
func ListFiles(workspaceDir, dockerfile string) ([]File, []string, error) {
	out, err := git(workspaceDir, "ls-files", "-co", "--exclude-standard", "-z")
	if err != nil {
		return nil, nil, err
	}
	var files []File
	var warnings []string
	seen := map[string]bool{}

	for _, path := range strings.Split(out, "\x00") {
		if path == "" || excluded(path) {
			continue
		}
		info, err := os.Lstat(filepath.Join(workspaceDir, filepath.FromSlash(path)))
		if err != nil {
			return nil, nil, err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			files = append(files, File{Path: path, Mode: os.ModeSymlink})
		case info.IsDir():
			warnings = append(warnings, "skipped "+path+": nested repository or submodule")
			continue
		case !info.Mode().IsRegular():
			warnings = append(warnings, "skipped "+path+": not a regular file")
			continue
		default:
			files = append(files, File{Path: path, Size: info.Size(), Mode: info.Mode()})
		}
		seen[path] = true
	}

	for _, extra := range []string{dockerfile, config.WorkspaceConfigFile} {
		extra = filepath.ToSlash(filepath.Clean(extra))
		if seen[extra] || extra == "." || strings.HasPrefix(extra, "../") {
			continue
		}
		info, err := os.Stat(filepath.Join(workspaceDir, filepath.FromSlash(extra)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		files = append(files, File{Path: extra, Size: info.Size(), Mode: info.Mode()})
		seen[extra] = true
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if err := checkSizes(files); err != nil {
		return nil, nil, err
	}
	for _, f := range files {
		if secretish(f.Path) {
			warnings = append(warnings, "check before publishing: "+f.Path)
		}
	}
	return files, warnings, nil
}

func excluded(path string) bool {
	if path == config.MarkerFile {
		return true
	}
	segments := strings.Split(path, "/")
	return excludedTopLevel[segments[0]] || slices.Contains(segments, ".git")
}

func checkSizes(files []File) error {
	var total int64
	for _, f := range files {
		if f.Size > maxFileSize {
			return fmt.Errorf("%s is %d MB; GitHub rejects files over 90 MB", f.Path, f.Size>>20)
		}
		total += f.Size
	}
	if total > maxTotalSize {
		return fmt.Errorf("the Workspace is %d MB; refusing to push more than 900 MB", total>>20)
	}
	return nil
}

// secretish flags files that commonly hold credentials. A Sync only warns: it
// is the user's call whether the file is really a secret.
func secretish(path string) bool {
	base := filepath.Base(path)
	switch {
	case !strings.Contains(path, "/") && (base == ".env" || strings.HasPrefix(base, ".env.")):
		return true
	case strings.HasSuffix(base, ".pem"),
		strings.HasPrefix(base, "id_rsa"):
		return true
	}
	return false
}

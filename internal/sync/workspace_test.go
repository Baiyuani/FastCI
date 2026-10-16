package sync

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Baiyuani/FastCI/internal/config"
)

func TestListFilesSelectsWhatTravels(t *testing.T) {
	dir := newRepo(t)
	mustWrite(t, filepath.Join(dir, ".gitignore"), "Dockerfile\nfastci.json\n")
	mustWrite(t, filepath.Join(dir, "app.py"), "x\n")
	// Ignored, but the build cannot run without them, so they travel anyway.
	mustWrite(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\n")
	mustWrite(t, filepath.Join(dir, "fastci.json"), "{}\n")
	// Environment scaffolding and anything that smells like a secret.
	mustWrite(t, filepath.Join(dir, ".codegraph", "codegraph.db"), "x\n")
	mustWrite(t, filepath.Join(dir, "id_rsa"), "key\n")
	mustWrite(t, filepath.Join(dir, "certs", "server.pem"), "cert\n")
	mustWrite(t, filepath.Join(dir, ".env"), "TOKEN=1\n")

	nested := filepath.Join(dir, "vendor", "nested")
	mustGit(t, nested, "init", "--quiet")
	mustWrite(t, filepath.Join(nested, "vendored.txt"), "nested\n")

	files, warnings, err := ListFiles(dir, "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}

	for _, want := range []string{"app.py", "Dockerfile", "fastci.json", ".gitignore", "id_rsa", "certs/server.pem"} {
		if !slices.Contains(paths, want) {
			t.Errorf("%s should travel; got %v", want, paths)
		}
	}
	for _, unwanted := range []string{".codegraph/codegraph.db", "vendor/nested"} {
		if slices.Contains(paths, unwanted) {
			t.Errorf("%s should not travel; got %v", unwanted, paths)
		}
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"id_rsa", "certs/server.pem", ".env", "vendor/nested"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings should mention %s:\n%s", want, joined)
		}
	}
}

func TestListFilesRefusesAnOversizedFile(t *testing.T) {
	dir := newRepo(t)
	big := filepath.Join(dir, "big.bin")
	mustWrite(t, big, "")
	// Sparse: a 91 MB file that costs almost nothing on disk.
	if err := os.Truncate(big, maxFileSize+1); err != nil {
		t.Fatal(err)
	}
	_, _, err := ListFiles(dir, "Dockerfile")
	if err == nil {
		t.Fatal("a 91 MB file should stop the Sync")
	}
	if !strings.Contains(err.Error(), "big.bin") {
		t.Errorf("the error should name the file, got: %v", err)
	}
}

func TestSecretishAndExcluded(t *testing.T) {
	travels := map[string]bool{
		".env":        true,
		".env.local":  true,
		"certs/x.pem": true,
		"id_rsa":      true,
		"id_rsa.pub":  true,
		"app/.env":    false, // only a root .env is the usual accident
		"src/main.go": false,
		"README.md":   false,
		"keys/x.key":  false,
	}
	for path, want := range travels {
		if got := secretish(path); got != want {
			t.Errorf("secretish(%q) = %v, want %v", path, got, want)
		}
	}

	skipped := map[string]bool{
		".git/config":      true,
		"src/.git/HEAD":    true,
		".codegraph/db":    true,
		".agents/skill.md": true,
		".codex/config":    true,
		config.MarkerFile:  true,
		"src/main.go":      false,
		".gitignore":       false,
	}
	for path, want := range skipped {
		if got := excluded(path); got != want {
			t.Errorf("excluded(%q) = %v, want %v", path, got, want)
		}
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "--quiet")
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

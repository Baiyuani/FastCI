package sync_test

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/nacl/box"

	"github.com/Baiyuani/FastCI/internal/config"
	"github.com/Baiyuani/FastCI/internal/github"
	fastcisync "github.com/Baiyuani/FastCI/internal/sync"
)

// fakeGitHub answers the API calls a Sync makes. Repositories are metadata; all
// commits live in one bare repo standing in for GitHub's storage, so the tests
// exercise real git end to end without a network.
type fakeGitHub struct {
	t      *testing.T
	bare   string
	url    string
	webURL string

	mu     sync.Mutex
	repos  map[string]*fakeRepo
	secret map[string]string

	public  [32]byte
	private *[32]byte
}

type fakeRepo struct {
	private bool
	runs    []github.Run
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{
		t:      t,
		webURL: "https://example.test",
		repos:  map[string]*fakeRepo{},
		secret: map[string]string{},
	}
	f.bare = filepath.Join(t.TempDir(), "remote.git")
	run(t, "", "git", "init", "--bare", "--quiet", f.bare)
	// A bare repo's HEAD defaults to master; point it at the branch FastCI writes.
	run(t, f.bare, "git", "symbolic-ref", "HEAD", "refs/heads/main")
	server := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(server.Close)
	f.url = server.URL
	return f
}

func (f *fakeGitHub) client() *github.Client {
	return github.New("test-token", f.url)
}

func (f *fakeGitHub) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.Split(path, "/")

	switch {
	case r.Method == http.MethodGet && path == "user":
		writeJSON(w, http.StatusOK, map[string]string{"login": "tester"})

	case r.Method == http.MethodPost && (path == "user/repos" || (len(parts) == 3 && parts[0] == "orgs")):
		var body struct {
			Name    string `json:"name"`
			Private bool   `json:"private"`
		}
		decodeBody(f.t, r, &body)
		owner := "tester"
		if parts[0] == "orgs" {
			owner = parts[1]
		}
		key := owner + "/" + body.Name
		f.repos[key] = &fakeRepo{private: body.Private}
		writeJSON(w, http.StatusCreated, f.repoJSON(key))

	case len(parts) >= 3 && parts[0] == "repos":
		key := parts[1] + "/" + parts[2]
		repo, ok := f.repos[key]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		switch {
		case len(parts) == 3 && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, f.repoJSON(key))
		case len(parts) == 3 && r.Method == http.MethodPatch:
			writeJSON(w, http.StatusOK, f.repoJSON(key))
		case len(parts) == 3 && r.Method == http.MethodDelete:
			delete(f.repos, key)
			w.WriteHeader(http.StatusNoContent)
		case len(parts) == 4 && parts[3] == "commits":
			if !f.hasCommits() {
				writeJSON(w, http.StatusConflict, map[string]string{"message": "Git Repository is empty."})
				return
			}
			writeJSON(w, http.StatusOK, []map[string]string{{"sha": f.head()}})
		case len(parts) == 4 && parts[3] == "actions":
			writeJSON(w, http.StatusOK, map[string]any{"workflow_runs": repo.runs})
		case len(parts) == 6 && parts[3] == "actions" && parts[4] == "secrets":
			f.handleSecrets(w, r, parts[5])
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		}

	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

func (f *fakeGitHub) handleSecrets(w http.ResponseWriter, r *http.Request, name string) {
	if name == "public-key" {
		public, private, err := box.GenerateKey(rand.Reader)
		if err != nil {
			f.t.Fatal(err)
		}
		f.public, f.private = *public, private
		writeJSON(w, http.StatusOK, map[string]string{
			"key_id": "test-key-id",
			"key":    base64.StdEncoding.EncodeToString(public[:]),
		})
		return
	}
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if f.private == nil {
		f.t.Fatal("a secret was written before the public key was read")
	}
	var body struct {
		EncryptedValue string `json:"encrypted_value"`
		KeyID          string `json:"key_id"`
	}
	decodeBody(f.t, r, &body)
	sealed, err := base64.StdEncoding.DecodeString(body.EncryptedValue)
	if err != nil {
		f.t.Fatal(err)
	}
	plain, ok := box.OpenAnonymous(nil, sealed, &f.public, f.private)
	if !ok {
		f.t.Fatal("the sealed secret did not decrypt with the repository key")
	}
	f.secret[name] = string(plain)
	w.WriteHeader(http.StatusCreated)
}

func (f *fakeGitHub) repoJSON(key string) map[string]any {
	return map[string]any{
		"full_name":      key,
		"html_url":       f.webURL + "/" + key,
		"clone_url":      f.bare,
		"private":        f.repos[key].private,
		"default_branch": "main",
	}
}

func (f *fakeGitHub) hasCommits() bool {
	return exec.Command("git", "-C", f.bare, "rev-parse", "--verify", "--quiet", "main").Run() == nil
}

func (f *fakeGitHub) head() string {
	return strings.TrimSpace(gitOut(f.t, f.bare, "rev-parse", "main"))
}

func (f *fakeGitHub) treeNames() string {
	return gitOut(f.t, f.bare, "ls-tree", "-r", "--name-only", "main")
}

func (f *fakeGitHub) show(path string) string {
	return gitOut(f.t, f.bare, "show", "main:"+path)
}

// pushHandEdit imitates someone editing the Target repo in the GitHub UI.
func (f *fakeGitHub) pushHandEdit(t *testing.T, file, content string) {
	t.Helper()
	dir := t.TempDir()
	run(t, "", "git", "clone", "--quiet", f.bare, dir)
	writeFile(t, filepath.Join(dir, file), content)
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "-c", "user.name=human", "-c", "user.email=human@example.test", "commit", "--quiet", "-m", "hand edit")
	run(t, dir, "git", "push", "--quiet", "origin", "main")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func decodeBody(t *testing.T, r *http.Request, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newWorkspace builds a Workspace holding the awkward cases: ignored files that
// must travel anyway, a nested repository, environment scaffolding, and a file
// that looks like a secret.
func newWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init", "--quiet")
	run(t, dir, "git", "symbolic-ref", "HEAD", "refs/heads/main")
	writeFile(t, filepath.Join(dir, ".gitignore"), "Dockerfile\nfastci.json\ntmp/\n")
	writeFile(t, filepath.Join(dir, "app.py"), "print('v1')\n")
	writeFile(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\n")
	writeFile(t, filepath.Join(dir, "fastci.json"), "{\"image\":\"ghcr.io/tester/app\"}\n")
	writeFile(t, filepath.Join(dir, ".codegraph", "codegraph.db"), "x\n")
	writeFile(t, filepath.Join(dir, ".env"), "SECRET=1\n")

	nested := filepath.Join(dir, "vendor", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, nested, "git", "init", "--quiet")
	writeFile(t, filepath.Join(nested, "vendored.txt"), "nested\n")
	run(t, nested, "git", "add", "-A")
	run(t, nested, "git", "-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "--quiet", "-m", "vendored")

	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "--quiet", "-m", "init")
	return dir
}

// settingsFor resolves a Workspace the way the push command does.
func settingsFor(t *testing.T, workspace string) config.Settings {
	t.Helper()
	wsConfig, err := config.LoadWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := config.Resolve("tester", workspace, config.Flags{Owner: "tester", Repo: "app"},
		wsConfig, config.Settings{}, config.Global{})
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

func newState(t *testing.T) *config.State {
	t.Helper()
	state, err := config.LoadState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func shortHead(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, dir, "rev-parse", "--short=7", "HEAD"))
}

func push(t *testing.T, fake *fakeGitHub, state *config.State, settings config.Settings) fastcisync.Result {
	t.Helper()
	result, err := fastcisync.Run(fake.client(), state, fastcisync.Options{
		Settings: settings, Mode: fastcisync.ModePush, Version: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

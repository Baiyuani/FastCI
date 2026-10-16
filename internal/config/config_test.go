package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolvePrecedence pins the order the user was promised: the global
// config, then the State entry from the last Sync, then the Workspace's
// fastci.json, then the command-line flags.
func TestResolvePrecedence(t *testing.T) {
	global := Global{Owner: "global", RegistryUsername: "global-user"}
	recorded := Settings{Owner: "recorded", Repo: "recorded", Dockerfile: "recorded.dockerfile"}
	ws := Workspace{Owner: "workspace", Repo: "workspace"}
	flag := Flags{Owner: "flag", Repo: "flag"}

	got, err := Resolve("login", "/tmp/app", flag, ws, recorded, global)
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "flag" || got.Repo != "flag" {
		t.Errorf("flags should win, got %s/%s", got.Owner, got.Repo)
	}
	// No flag and no fastci.json named a Dockerfile, so the record fills it in.
	if got.Dockerfile != "recorded.dockerfile" {
		t.Errorf("Dockerfile = %q, want the recorded one", got.Dockerfile)
	}

	got, err = Resolve("login", "/tmp/app", Flags{}, ws, recorded, global)
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "workspace" || got.Repo != "workspace" {
		t.Errorf("fastci.json should beat the record and the global config, got %s/%s", got.Owner, got.Repo)
	}

	got, err = Resolve("login", "/tmp/app", Flags{}, Workspace{}, recorded, global)
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "recorded" {
		t.Errorf("the record should beat the global config, got %q", got.Owner)
	}

	got, err = Resolve("login", "/tmp/app", Flags{}, Workspace{}, Settings{}, global)
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "global" {
		t.Errorf("the global config should beat the login, got %q", got.Owner)
	}
	if got.RegistryUsername != "global-user" {
		t.Errorf("RegistryUsername = %q", got.RegistryUsername)
	}

	got, err = Resolve("login", "/tmp/app", Flags{}, Workspace{}, Settings{}, Global{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "login" {
		t.Errorf("the login is the last resort, got %q", got.Owner)
	}
	// Nothing named the repo, so the Workspace directory does.
	if got.Repo != "app" || got.Image != "ghcr.io/login/app" {
		t.Errorf("defaults = %q / %q, want app / ghcr.io/login/app", got.Repo, got.Image)
	}
	if got.Dockerfile != DefaultDockerfile || got.Workflow != DefaultWorkflow {
		t.Errorf("defaults = %q / %q", got.Dockerfile, got.Workflow)
	}
	if !got.UsesGitHubToken() {
		t.Error("ghcr.io with no credentials should log in with GITHUB_TOKEN")
	}
}

func TestResolveRejectsBadInput(t *testing.T) {
	baseline := Flags{Owner: "o", Repo: "r", Image: "ghcr.io/o/r"}
	for _, tc := range []struct {
		name string
		flag Flags
	}{
		{"no owner", Flags{Repo: "r", Image: "ghcr.io/o/r"}},
		{"owner/repo in --repo", Flags{Owner: "o", Repo: "o/r", Image: "ghcr.io/o/r"}},
		{"image without a registry host", Flags{Owner: "o", Repo: "r", Image: "app"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Resolve("", "/tmp/app", tc.flag, Workspace{}, Settings{}, Global{}); err == nil {
				t.Error("Resolve should have refused this")
			}
		})
	}
	if _, err := Resolve("login", "/tmp/app", baseline, Workspace{}, Settings{}, Global{}); err != nil {
		t.Errorf("the baseline should resolve: %v", err)
	}
}

func TestResolveTagsReplacesTheRecord(t *testing.T) {
	// --tags is comma-separated, and it replaces both the record and fastci.json.
	got, err := Resolve("login", "/tmp/app",
		Flags{Owner: "o", Repo: "r", Image: "ghcr.io/o/r", Tags: " v1, latest ,"},
		Workspace{Tags: []string{"from-file"}}, Settings{Tags: []string{"recorded"}}, Global{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "v1" || got.Tags[1] != "latest" {
		t.Errorf("Tags = %v, want [v1 latest]", got.Tags)
	}
}

func TestStateRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	state, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Entries()) != 0 {
		t.Fatal("a State file that does not exist yet should load empty")
	}

	state.Put("b/two", Entry{Owner: "b", Repo: "two", Workspace: "/tmp/two"})
	state.Put("a/one", Entry{Owner: "a", Repo: "one", Workspace: "/tmp/one/"})
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the State file is %o; it can hold a token, so it should be 600", perm)
	}

	reloaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if entry, ok := reloaded.Get("a/one"); !ok || entry.Repo != "one" {
		t.Errorf("Get(a/one) = %+v, %v", entry, ok)
	}
	if key, _, ok := reloaded.ByWorkspace("/tmp/one"); !ok || key != "a/one" {
		t.Errorf("ByWorkspace should ignore a trailing slash, got %q, %v", key, ok)
	}
	if _, _, ok := reloaded.ByWorkspace("/tmp/missing"); ok {
		t.Error("an unknown Workspace should not match")
	}
	if entries := reloaded.Entries(); len(entries) != 2 || entries[0].Repo != "one" || entries[1].Repo != "two" {
		t.Errorf("Entries() should be ordered by key, got %+v", entries)
	}

	reloaded.Remove("a/one")
	if _, ok := reloaded.Get("a/one"); ok {
		t.Error("Remove should drop the record")
	}
}

func TestUnknownWorkspaceConfigKeysAreRejected(t *testing.T) {
	dir := t.TempDir()
	// fastci.json travels into the Target repo, so a typo must not be mistaken
	// for a supported key, and a credential there must be refused outright.
	if err := os.WriteFile(filepath.Join(dir, WorkspaceConfigFile),
		[]byte(`{"image":"ghcr.io/o/r","token":"nope"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkspace(dir); err == nil {
		t.Error("a credential-looking key should be rejected, not silently ignored")
	}
}

package sync_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Baiyuani/FastCI/internal/config"
	fastcisync "github.com/Baiyuani/FastCI/internal/sync"
)

func TestPushCreatesTheTargetRepo(t *testing.T) {
	workspace := newWorkspace(t)
	fake := newFakeGitHub(t)
	state := newState(t)
	settings := settingsFor(t, workspace)

	result := push(t, fake, state, settings)

	if !result.Changed {
		t.Error("the first Sync should report a change")
	}
	if want := fake.webURL + "/tester/app"; result.RepoURL != want {
		t.Errorf("RepoURL = %q, want %q", result.RepoURL, want)
	}
	repo := fake.repos["tester/app"]
	if repo == nil {
		t.Fatal("the Target repo was never created")
	}
	if !repo.private {
		t.Error("the Target repo should be private by default")
	}

	tree := fake.treeNames()
	// .env travels: a secret-looking file warns, it does not block.
	for _, want := range []string{"app.py", "Dockerfile", "fastci.json", ".gitignore", ".env", ".github/workflows/fastci-build.yml"} {
		if !strings.Contains(tree, want) {
			t.Errorf("the Target repo is missing %s; it holds:\n%s", want, tree)
		}
	}
	for _, unwanted := range []string{".codegraph", "vendor/nested"} {
		if strings.Contains(tree, unwanted) {
			t.Errorf("the Target repo should not carry %s; it holds:\n%s", unwanted, tree)
		}
	}

	workflow := fake.show(".github/workflows/fastci-build.yml")
	for _, want := range []string{
		"registry: ghcr.io",
		"username: ${{ github.actor }}",
		"password: ${{ secrets.GITHUB_TOKEN }}",
		"file: Dockerfile",
		"ghcr.io/tester/app:latest",
		"ghcr.io/tester/app:sha-" + shortHead(t, workspace),
		"ghcr.io/tester/app:${{ inputs.tag }}",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("the workflow should contain %q:\n%s", want, workflow)
		}
	}

	marker := fake.show(".fastci.json")
	if strings.Contains(marker, workspace) {
		t.Errorf("the marker must not leak the local path:\n%s", marker)
	}
	var parsed config.Marker
	if err := json.Unmarshal([]byte(marker), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.WorkspaceCommit != shortHead(t, workspace) {
		t.Errorf("marker workspace_commit = %q, want %q", parsed.WorkspaceCommit, shortHead(t, workspace))
	}
	if parsed.Fingerprint == "" {
		t.Error("the marker should carry a fingerprint")
	}

	entry, ok := state.Get("tester/app")
	if !ok {
		t.Fatal("the Sync did not record the Target repo")
	}
	if entry.LastSyncSHA != fake.head() {
		t.Errorf("recorded LastSyncSHA = %q, pushed %q", entry.LastSyncSHA, fake.head())
	}
	if len(result.Warnings) == 0 {
		t.Error("the nested repo and the .env file should have produced warnings")
	}
}

func TestUpdateAddsOneCommitPerChange(t *testing.T) {
	workspace := newWorkspace(t)
	fake := newFakeGitHub(t)
	state := newState(t)
	settings := settingsFor(t, workspace)
	push(t, fake, state, settings)
	first := fake.head()

	writeFile(t, filepath.Join(workspace, "app.py"), "print('v2')\n")
	run(t, workspace, "git", "add", "-A")
	run(t, workspace, "git", "-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "--quiet", "-m", "v2")

	result, err := fastcisync.Run(fake.client(), state, fastcisync.Options{
		Settings: settings, Mode: fastcisync.ModeUpdate, Version: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Error("changing the Workspace should change the Target repo")
	}
	if parent := strings.TrimSpace(gitOut(t, fake.bare, "rev-parse", "main^")); parent != first {
		t.Errorf("the Sync commit's parent = %q, want the previous Sync %q", parent, first)
	}
	if !strings.Contains(fake.show("app.py"), "v2") {
		t.Error("the new commit should carry the updated file")
	}

	before := fake.head()
	second, err := fastcisync.Run(fake.client(), state, fastcisync.Options{
		Settings: settings, Mode: fastcisync.ModeUpdate, Version: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed {
		t.Error("an unchanged Workspace should not produce a commit")
	}
	if after := fake.head(); after != before {
		t.Errorf("a no-op update moved the branch from %q to %q", before, after)
	}
}

func TestUpdateRefusesHandEditsUnlessForced(t *testing.T) {
	workspace := newWorkspace(t)
	fake := newFakeGitHub(t)
	state := newState(t)
	settings := settingsFor(t, workspace)
	push(t, fake, state, settings)

	fake.pushHandEdit(t, "README.md", "edited in the GitHub UI\n")

	_, err := fastcisync.Run(fake.client(), state, fastcisync.Options{
		Settings: settings, Mode: fastcisync.ModeUpdate, Version: "test",
	})
	if err == nil {
		t.Fatal("update should refuse a Target repo that changed outside FastCI")
	}
	if !strings.Contains(err.Error(), "outside FastCI") {
		t.Errorf("the error should explain the divergence, got: %v", err)
	}

	result, err := fastcisync.Run(fake.client(), state, fastcisync.Options{
		Settings: settings, Mode: fastcisync.ModeUpdate, Force: true, Version: "test",
	})
	if err != nil {
		t.Fatalf("--force should overwrite: %v", err)
	}
	if !result.Changed {
		t.Error("the forced update should push a commit")
	}
	if strings.Contains(fake.treeNames(), "README.md") {
		t.Error("--force should make the Target repo match the Workspace exactly")
	}
}

func TestPushAdoptsTheEmptyRepoLeftByAFailedRun(t *testing.T) {
	workspace := newWorkspace(t)
	fake := newFakeGitHub(t)
	fake.repos["tester/app"] = &fakeRepo{private: true}
	state := newState(t)

	result := push(t, fake, state, settingsFor(t, workspace))

	if !result.Changed {
		t.Error("adopting an empty repo should still push a commit")
	}
	if _, ok := state.Get("tester/app"); !ok {
		t.Error("the adopted repo should be recorded")
	}
}

func TestPushRefusesANonEmptyRepo(t *testing.T) {
	workspace := newWorkspace(t)
	fake := newFakeGitHub(t)
	state := newState(t)
	settings := settingsFor(t, workspace)
	push(t, fake, state, settings)

	state.Remove("tester/app") // as if this machine had never heard of it

	_, err := fastcisync.Run(fake.client(), state, fastcisync.Options{
		Settings: settings, Mode: fastcisync.ModePush, Version: "test",
	})
	if err == nil || !strings.Contains(err.Error(), "fastci update") {
		t.Fatalf("push over a non-empty repo should point at update, got: %v", err)
	}
}

func TestSyncWithCustomRegistryWritesSecrets(t *testing.T) {
	workspace := newWorkspace(t)
	fake := newFakeGitHub(t)
	state := newState(t)
	settings := settingsFor(t, workspace)
	settings.Image = "registry.example.com/team/app"
	settings.RegistryUsername = "robot"
	settings.RegistryPassword = "hunter2"

	push(t, fake, state, settings)

	if got := fake.secret["REGISTRY_USERNAME"]; got != "robot" {
		t.Errorf("REGISTRY_USERNAME = %q", got)
	}
	if got := fake.secret["REGISTRY_PASSWORD"]; got != "hunter2" {
		t.Errorf("REGISTRY_PASSWORD = %q", got)
	}
	workflow := fake.show(".github/workflows/fastci-build.yml")
	if !strings.Contains(workflow, "registry: registry.example.com") {
		t.Errorf("the workflow should log in to the custom registry:\n%s", workflow)
	}
	if !strings.Contains(workflow, "password: ${{ secrets.REGISTRY_PASSWORD }}") {
		t.Errorf("the workflow should read the registry password from secrets:\n%s", workflow)
	}
	if strings.Contains(workflow, "hunter2") {
		t.Error("the password must never be written into the workflow file")
	}
}

func TestSyncRefusesAMissingDockerfileBeforeCreatingAnything(t *testing.T) {
	workspace := newWorkspace(t)
	if err := os.Remove(filepath.Join(workspace, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	fake := newFakeGitHub(t)
	state := newState(t)
	settings := settingsFor(t, workspace)
	settings.Repo = "no-dockerfile"
	settings.Image = "ghcr.io/tester/no-dockerfile"

	if _, err := fastcisync.Run(fake.client(), state, fastcisync.Options{
		Settings: settings, Mode: fastcisync.ModePush, Version: "test",
	}); err == nil {
		t.Fatal("a missing Dockerfile should fail the Sync")
	}
	if fake.repos["tester/no-dockerfile"] != nil {
		t.Error("nothing may be created on GitHub before the Dockerfile is known good")
	}
	if len(state.Entries()) != 0 {
		t.Error("a failed Sync should not be recorded")
	}
}

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Baiyuani/FastCI/internal/config"
)

// testEnv is the directory holding the config and state a command run will
// read, plus the environment that keeps the run offline.
func testEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("GITHUB_TOKEN", "test-token")
	// Nothing routable: every command below must reach only the local State.
	t.Setenv("FASTCI_API_URL", "http://127.0.0.1:1")
	dir := t.TempDir()
	// An explicitly named config file has to exist, so give it an empty one.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runCLI invokes one command the way the process would.
func runCLI(t *testing.T, dir, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"--config", filepath.Join(dir, "config.json")}, args...))
	err := cmd.Execute()
	return out.String(), err
}

// seed records Target repos in the State file the command run will read.
func seed(t *testing.T, dir string, entries ...config.Entry) {
	t.Helper()
	state, err := config.LoadState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		state.Put(entry.Owner+"/"+entry.Repo, entry)
	}
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestListPrintsJSON(t *testing.T) {
	dir := testEnv(t)
	seed(t, dir, config.Entry{Owner: "tester", Repo: "app", Workspace: "/w/app", Image: "ghcr.io/tester/app"})

	out, err := runCLI(t, dir, "", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var statuses []struct {
		Repo      string   `json:"repo"`
		ImageRefs []string `json:"image_refs"`
	}
	if err := json.Unmarshal([]byte(out), &statuses); err != nil {
		t.Fatalf("%v in %q", err, out)
	}
	if len(statuses) != 1 || statuses[0].Repo != "tester/app" {
		t.Fatalf("statuses = %+v", statuses)
	}
	// The unreachable API costs the run fields but not the record.
	if len(statuses[0].ImageRefs) == 0 {
		t.Error("list should report the image tags even when the API is down")
	}
}

func TestListIsEmptyWithoutRecords(t *testing.T) {
	out, err := runCLI(t, testEnv(t), "", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("list --json = %q, want []", out)
	}
}

func TestMissingTokenIsReported(t *testing.T) {
	dir := testEnv(t)
	t.Setenv("GITHUB_TOKEN", "")
	_, err := runCLI(t, dir, "", "list")
	if err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Fatalf("err = %v, want a hint about GITHUB_TOKEN", err)
	}
}

func TestUnknownTargetIsRejected(t *testing.T) {
	dir := testEnv(t)
	missing := filepath.Join(dir, "nope")
	if _, err := runCLI(t, dir, "", "status", missing); err == nil {
		t.Error("status should refuse a path that is not a Workspace")
	}
	if _, err := runCLI(t, dir, "", "push", missing); err == nil {
		t.Error("push should refuse a path that is not a Workspace")
	}
}

func TestDeleteNeedsTheNameTypedOut(t *testing.T) {
	dir := testEnv(t)
	seed(t, dir, config.Entry{Owner: "tester", Repo: "app", Workspace: "/w/app"})

	if _, err := runCLI(t, dir, "app\nyes\n", "delete", "tester/app"); err == nil {
		t.Error("delete should abort when the typed name does not match")
	}

	// --yes against an unreachable API fails, and a failed delete must not drop
	// the record: the repository is still there to try again.
	out, err := runCLI(t, dir, "", "delete", "--yes", "tester/app")
	if err == nil {
		t.Fatalf("delete against an unreachable API should fail, got %q", out)
	}
	if strings.Contains(out, "deleted ") {
		t.Error("delete reported success without deleting anything")
	}
	state, err := config.LoadState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Get("tester/app"); !ok {
		t.Error("a failed delete should keep the record so it can be retried")
	}
}

func TestDeleteWithNoRecord(t *testing.T) {
	dir := testEnv(t)
	if _, err := runCLI(t, dir, "", "delete", "--yes", filepath.Join(dir, "app")); err == nil {
		t.Error("delete should refuse a Workspace it has no record of")
	}
}

func TestConfigFileMustExistWhenNamed(t *testing.T) {
	dir := testEnv(t)
	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", filepath.Join(dir, "missing.json"), "list"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want a complaint about the named config file", err)
	}
}

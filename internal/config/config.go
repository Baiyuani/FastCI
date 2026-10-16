// Package config loads FastCI's settings: the global config file, a
// Workspace's fastci.json, and the Settings a Sync resolves from both.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultDockerfile   = "Dockerfile"
	DefaultWorkflow     = "fastci-build.yml"
	DefaultRegistry     = "ghcr.io"
	WorkspaceConfigFile = "fastci.json"
)

// Global is the machine-wide config. It holds credentials, so it is the one
// config file that never reaches a repository.
type Global struct {
	Token            string `json:"token"`
	Owner            string `json:"owner"`
	RegistryUsername string `json:"registry_username"`
	RegistryPassword string `json:"registry_password"`
}

// Workspace is a Workspace's optional fastci.json. FastCI copies it into the
// Target repo, so it must never hold credentials: unknown keys are rejected and
// no field here is secret.
type Workspace struct {
	Owner      string   `json:"owner"`
	Repo       string   `json:"repo"`
	Image      string   `json:"image"`
	Dockerfile string   `json:"dockerfile"`
	Tags       []string `json:"tags"`
	Workflow   string   `json:"workflow"`
	Public     bool     `json:"public"`
}

// Flags carries only what the user typed on the command line. An empty value
// means "not set", so a lower-precedence source can still fill it in.
type Flags struct {
	Owner            string
	Repo             string
	Image            string
	Dockerfile       string
	Tags             string
	Workflow         string
	Public           bool
	RegistryUsername string
	RegistryPassword string
}

// Settings is one Sync's resolved configuration.
type Settings struct {
	Workspace        string
	Owner            string
	Repo             string
	Image            string
	Dockerfile       string
	Tags             []string
	Workflow         string
	Public           bool
	Token            string
	RegistryUsername string
	RegistryPassword string
}

// Key is the Target repo's "owner/name".
func (s Settings) Key() string { return s.Owner + "/" + s.Repo }

// Host is the registry host the image lives on, used for the login step.
func (s Settings) Host() string { return strings.SplitN(s.Image, "/", 2)[0] }

// UsesGitHubToken reports whether the build logs in with the run's
// GITHUB_TOKEN instead of registry secrets.
func (s Settings) UsesGitHubToken() bool {
	return s.Host() == DefaultRegistry && s.RegistryUsername == "" && s.RegistryPassword == ""
}

// DefaultConfigPath is the global config's conventional location.
func DefaultConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "fastci", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(home, ".config", "fastci", "config.json")
}

// LoadGlobal reads the global config. A missing file is only an error when the
// caller named it explicitly.
func LoadGlobal(path string, required bool) (Global, error) {
	var g Global
	if err := readJSON(path, &g, required); err != nil {
		return Global{}, err
	}
	return g, nil
}

// LoadWorkspace reads a Workspace's fastci.json; it is optional.
func LoadWorkspace(dir string) (Workspace, error) {
	var w Workspace
	if err := readJSON(filepath.Join(dir, WorkspaceConfigFile), &w, false); err != nil {
		return Workspace{}, err
	}
	return w, nil
}

func readJSON(path string, v any, required bool) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if required {
			return fmt.Errorf("config file %s does not exist", path)
		}
		return nil
	}
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Resolve merges every source of configuration. Later sources win: the global
// config, then the State entry from the last Sync, then the Workspace's
// fastci.json, then the command-line flags.
func Resolve(login, workspacePath string, flag Flags, ws Workspace, recorded Settings, global Global) (Settings, error) {
	s := Settings{
		Workspace:        workspacePath,
		Owner:            firstNonEmpty(flag.Owner, ws.Owner, recorded.Owner, global.Owner, login),
		Repo:             firstNonEmpty(flag.Repo, ws.Repo, recorded.Repo, filepath.Base(workspacePath)),
		Dockerfile:       firstNonEmpty(flag.Dockerfile, ws.Dockerfile, recorded.Dockerfile, DefaultDockerfile),
		Workflow:         firstNonEmpty(flag.Workflow, ws.Workflow, recorded.Workflow, DefaultWorkflow),
		Public:           flag.Public || ws.Public,
		Token:            firstNonEmpty(os.Getenv("GITHUB_TOKEN"), global.Token),
		RegistryUsername: firstNonEmpty(flag.RegistryUsername, global.RegistryUsername),
		RegistryPassword: firstNonEmpty(flag.RegistryPassword, global.RegistryPassword),
		Tags:             firstNonEmptyList(parseTags(flag.Tags), ws.Tags, recorded.Tags),
	}
	s.Image = firstNonEmpty(flag.Image, ws.Image, recorded.Image, DefaultRegistry+"/"+s.Owner+"/"+s.Repo)

	switch {
	case s.Owner == "":
		return Settings{}, errors.New("no GitHub owner: pass --owner or set \"owner\" in the global config")
	case s.Repo == "":
		return Settings{}, errors.New("no Target repo name: pass --repo")
	case strings.Contains(s.Repo, "/"):
		return Settings{}, fmt.Errorf("--repo takes a bare name (%q given); use --owner for the account", s.Repo)
	case !strings.Contains(s.Image, "/"):
		return Settings{}, fmt.Errorf("image %q needs a registry host, e.g. ghcr.io/%s/%s", s.Image, s.Owner, s.Repo)
	}
	return s, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmptyList(values ...[]string) []string {
	for _, v := range values {
		if len(v) > 0 {
			return v
		}
	}
	return nil
}

func parseTags(s string) []string {
	var tags []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			tags = append(tags, part)
		}
	}
	return tags
}

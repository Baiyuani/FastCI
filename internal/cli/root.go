// Package cli wires the fastci commands onto the packages that do the work.
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Baiyuani/FastCI/internal/config"
	"github.com/Baiyuani/FastCI/internal/github"
)

// Version is stamped at build time: -ldflags "-X .../internal/cli.Version=v1.2.3".
var Version = "dev"

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "fastci: %v\n", err)
		return 1
	}
	return 0
}

// env is the configuration and state every command shares.
type env struct {
	configPath string
	global     config.Global
	state      *config.State
}

func newRoot() *cobra.Command {
	e := &env{}
	root := &cobra.Command{
		Use:           "fastci",
		Short:         "Mirror a Workspace into a GitHub repo that builds and publishes its image",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&e.configPath, "config", "",
		"global config file (default ~/.config/fastci/config.json)")
	root.AddCommand(
		newPushCmd(e),
		newUpdateCmd(e),
		newListCmd(e),
		newStatusCmd(e),
		newOpenCmd(e),
		newDeleteCmd(e),
	)
	return root
}

// load reads the global config and the local State file once per run.
func (e *env) load() error {
	if e.state != nil {
		return nil
	}
	path := e.configPath
	explicit := path != ""
	if !explicit {
		path = config.DefaultConfigPath()
	}
	global, err := config.LoadGlobal(path, explicit)
	if err != nil {
		return err
	}
	state, err := config.LoadState(filepath.Join(filepath.Dir(path), "state.json"))
	if err != nil {
		return err
	}
	e.global, e.state = global, state
	return nil
}

func (e *env) token() (string, error) {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t, nil
	}
	if e.global.Token != "" {
		return e.global.Token, nil
	}
	return "", fmt.Errorf("no GitHub token: set GITHUB_TOKEN, or \"token\" in %s", e.configFile())
}

func (e *env) configFile() string {
	if e.configPath != "" {
		return e.configPath
	}
	return config.DefaultConfigPath()
}

func (e *env) client() (*github.Client, error) {
	token, err := e.token()
	if err != nil {
		return nil, err
	}
	base := os.Getenv("FASTCI_API_URL")
	if base == "" {
		base = github.DefaultBaseURL
	}
	return github.New(token, base), nil
}

func (e *env) saveState() error {
	return e.state.Save()
}

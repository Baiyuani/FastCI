package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Baiyuani/FastCI/internal/config"
	"github.com/Baiyuani/FastCI/internal/github"
	"github.com/Baiyuani/FastCI/internal/sync"
)

// repoStatus is what list and status print, in text or as --json.
type repoStatus struct {
	Repo              string   `json:"repo"`
	URL               string   `json:"url,omitempty"`
	Workspace         string   `json:"workspace"`
	Image             string   `json:"image"`
	ImageRefs         []string `json:"image_refs"`
	LastSyncSHA       string   `json:"last_sync_sha"`
	LastSyncAt        string   `json:"last_sync_at"`
	WorkspaceSHA      string   `json:"workspace_sha"`
	LastRunURL        string   `json:"last_run_url,omitempty"`
	LastRunStatus     string   `json:"last_run_status,omitempty"`
	LastRunConclusion string   `json:"last_run_conclusion,omitempty"`
}

// syncFlags is the configuration every Sync accepts.
type syncFlags struct {
	config.Flags
	force bool
}

func (f *syncFlags) register(cmd *cobra.Command, withForce bool) {
	fl := cmd.Flags()
	fl.StringVar(&f.Owner, "owner", "", "GitHub account or org that owns the Target repo")
	fl.StringVar(&f.Repo, "repo", "", "Target repo name (default: the Workspace directory name)")
	fl.StringVar(&f.Image, "image", "", "image reference without a tag (default ghcr.io/<owner>/<repo>)")
	fl.StringVar(&f.Dockerfile, "dockerfile", "", "Dockerfile inside the Workspace (default Dockerfile)")
	fl.StringVar(&f.Tags, "tags", "", "comma-separated image tags, replacing latest; sha-<commit> is always added")
	fl.StringVar(&f.Workflow, "workflow", "", "workflow file name under .github/workflows (default fastci-build.yml)")
	fl.BoolVar(&f.Public, "public", false, "create the Target repo as public")
	fl.StringVar(&f.RegistryUsername, "registry-username", "", "registry username, stored as the REGISTRY_USERNAME secret")
	fl.StringVar(&f.RegistryPassword, "registry-password", "", "registry password, stored as the REGISTRY_PASSWORD secret")
	if withForce {
		fl.BoolVar(&f.force, "force", false, "overwrite a Target repo that changed outside FastCI")
	}
}

func newPushCmd(e *env) *cobra.Command {
	var f syncFlags
	cmd := &cobra.Command{
		Use:   "push [workspace]",
		Short: "Create the Target repo for a Workspace and sync it",
		Long: "Create the Target repo for a Workspace, inject the workflow, and push it.\n" +
			"Every check runs before the repository is created; if the push itself fails,\n" +
			"running push again picks up the empty repository it left behind.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd, e, firstArg(args), f, sync.ModePush)
		},
	}
	f.register(cmd, false)
	return cmd
}

func newUpdateCmd(e *env) *cobra.Command {
	var f syncFlags
	cmd := &cobra.Command{
		Use:   "update [workspace|owner/repo]",
		Short: "Sync a Workspace into its Target repo again",
		Long: "Copy the Workspace over its Target repo as one new commit, re-rendering the\n" +
			"workflow. If the repo changed outside FastCI, update stops instead of\n" +
			"overwriting it; --force overrides that.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd, e, firstArg(args), f, sync.ModeUpdate)
		},
	}
	f.register(cmd, true)
	return cmd
}

func runSync(cmd *cobra.Command, e *env, target string, f syncFlags, mode sync.Mode) error {
	if err := e.load(); err != nil {
		return err
	}
	gh, err := e.client()
	if err != nil {
		return err
	}
	dir, recorded, err := e.resolveTarget(target)
	if err != nil {
		return err
	}
	if mode == sync.ModeUpdate && recorded == nil {
		return fmt.Errorf("no Target repo recorded for %s; run `fastci push` first", target)
	}
	if recorded != nil {
		// Naming the repo pins the identity, so a flag cannot quietly redirect
		// the Sync at a different repository.
		if f.Owner != "" && f.Owner != recorded.Owner {
			return fmt.Errorf("--owner %s disagrees with the recorded owner %s", f.Owner, recorded.Owner)
		}
		if f.Repo != "" && f.Repo != recorded.Repo {
			return fmt.Errorf("--repo %s disagrees with the recorded repo %s", f.Repo, recorded.Repo)
		}
		f.Owner, f.Repo = recorded.Owner, recorded.Repo
	}

	wsConfig, err := config.LoadWorkspace(dir)
	if err != nil {
		return err
	}
	login, err := gh.Login()
	if err != nil {
		return err
	}
	var previous config.Settings
	if recorded != nil {
		previous = recorded.Settings()
	}
	settings, err := config.Resolve(login, dir, f.Flags, wsConfig, previous, e.global)
	if err != nil {
		return err
	}

	result, err := sync.Run(gh, e.state, sync.Options{
		Settings: settings,
		Mode:     mode,
		Force:    f.force,
		Version:  Version,
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	for _, warning := range result.Warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
	fmt.Fprintf(out, "%s %s\n", settings.Key(), result.RepoURL)
	if !result.Changed {
		fmt.Fprintln(out, "no changes to sync")
	}
	fmt.Fprintf(out, "workflow runs: %s/actions\n", result.RepoURL)
	for _, ref := range result.ImageRefs {
		fmt.Fprintf(out, "image: %s\n", ref)
	}
	return nil
}

func newListCmd(e *env) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the Target repos this machine created",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := e.load(); err != nil {
				return err
			}
			gh, err := e.client()
			if err != nil {
				return err
			}
			statuses := []repoStatus{}
			for _, entry := range e.state.Entries() {
				statuses = append(statuses, describe(gh, entry))
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, statuses)
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "REPO\tWORKSPACE\tLAST SYNC\tLAST RUN")
			for _, s := range statuses {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.Repo, s.Workspace, s.LastSyncSHA, runSummary(s))
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func newStatusCmd(e *env) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status [workspace|owner/repo]",
		Short: "Show one Target repo, its last sync, and its last CI run",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := e.load(); err != nil {
				return err
			}
			gh, err := e.client()
			if err != nil {
				return err
			}
			target := firstArg(args)
			_, entry, err := e.resolveTarget(target)
			if err != nil {
				return err
			}
			if entry == nil {
				return fmt.Errorf("no Target repo recorded for %s; run `fastci push` first", target)
			}
			status := describe(gh, *entry)
			repo, exists, err := gh.GetRepo(entry.Owner, entry.Repo)
			if err != nil {
				return err
			}
			if exists {
				status.URL = repo.HTMLURL
			} else {
				status.LastRunURL = ""
				status.LastRunStatus = "gone"
			}

			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, status)
			}
			fmt.Fprintf(out, "%s\n", status.Repo)
			fmt.Fprintf(out, "  workspace:  %s\n", status.Workspace)
			if status.URL != "" {
				fmt.Fprintf(out, "  repo:       %s\n", status.URL)
			}
			fmt.Fprintf(out, "  last sync:  %s (workspace %s) at %s\n", status.LastSyncSHA, status.WorkspaceSHA, status.LastSyncAt)
			for _, ref := range status.ImageRefs {
				fmt.Fprintf(out, "  image:      %s\n", ref)
			}
			fmt.Fprintf(out, "  last run:   %s %s\n", runSummary(status), status.LastRunURL)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func newOpenCmd(e *env) *cobra.Command {
	var actions, run bool
	cmd := &cobra.Command{
		Use:   "open [workspace|owner/repo]",
		Short: "Open a Target repo, its runs, or its latest run in the browser",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := e.load(); err != nil {
				return err
			}
			gh, err := e.client()
			if err != nil {
				return err
			}
			_, entry, err := e.resolveTarget(firstArg(args))
			if err != nil {
				return err
			}
			if entry == nil {
				return fmt.Errorf("no Target repo recorded for %s", firstArg(args))
			}
			repo, exists, err := gh.GetRepo(entry.Owner, entry.Repo)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%s/%s is gone from GitHub", entry.Owner, entry.Repo)
			}
			url := repo.HTMLURL
			switch {
			case run:
				latest, ok, err := gh.LatestRun(entry.Owner, entry.Repo, sync.DefaultBranch)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("%s/%s has no workflow runs yet", entry.Owner, entry.Repo)
				}
				url = latest.HTMLURL
			case actions:
				url += "/actions"
			}
			return openBrowser(url)
		},
	}
	cmd.Flags().BoolVar(&actions, "actions", false, "open the Actions tab")
	cmd.Flags().BoolVar(&run, "run", false, "open the latest workflow run")
	return cmd
}

func newDeleteCmd(e *env) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete [workspace|owner/repo]",
		Short: "Delete a Target repo on GitHub and forget it",
		Long: "Delete the Target repo and drop its record. The image package is not\n" +
			"deleted: GitHub keeps packages separate from repositories.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := e.load(); err != nil {
				return err
			}
			gh, err := e.client()
			if err != nil {
				return err
			}
			_, entry, err := e.resolveTarget(firstArg(args))
			if err != nil {
				return err
			}
			if entry == nil {
				return fmt.Errorf("no Target repo recorded for %s", firstArg(args))
			}
			key := entry.Owner + "/" + entry.Repo
			out := cmd.OutOrStdout()
			if !yes {
				fmt.Fprintf(out, "Type %s to delete it: ", key)
				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && !errors.Is(err, io.EOF) {
					return err
				}
				if strings.TrimSpace(line) != key {
					return errors.New("aborted: the name did not match")
				}
			}
			if err := gh.DeleteRepo(entry.Owner, entry.Repo); err != nil && !errors.Is(err, github.ErrNotFound) {
				return err
			}
			e.state.Remove(key)
			if err := e.saveState(); err != nil {
				return err
			}
			fmt.Fprintf(out, "deleted %s\n", key)
			fmt.Fprintln(out, "note: images already published are still in GitHub Packages; delete them there if you want them gone")
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "delete without typing the repository name")
	return cmd
}

// resolveTarget accepts a Workspace directory or an "owner/repo" recorded in the
// State file, and returns the directory plus the record when there is one.
func (e *env) resolveTarget(arg string) (string, *config.Entry, error) {
	if entry, ok := e.state.Get(arg); ok {
		return entry.Workspace, &entry, nil
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", nil, fmt.Errorf("%s is neither a Workspace directory nor a Target repo recorded in %s", arg, e.state.Path())
	}
	if _, entry, ok := e.state.ByWorkspace(abs); ok {
		return abs, &entry, nil
	}
	return abs, nil, nil
}

// describe reports what the State file plus the latest run say about a Target
// repo. A failed API call leaves the run fields empty instead of failing list.
func describe(gh *github.Client, entry config.Entry) repoStatus {
	status := repoStatus{
		Repo:         entry.Owner + "/" + entry.Repo,
		Workspace:    entry.Workspace,
		Image:        entry.Image,
		ImageRefs:    sync.ImageRefs(entry.Image, entry.Tags, entry.WorkspaceSHA),
		LastSyncSHA:  entry.LastSyncSHA,
		LastSyncAt:   entry.LastSyncAt,
		WorkspaceSHA: entry.WorkspaceSHA,
	}
	if latest, ok, err := gh.LatestRun(entry.Owner, entry.Repo, sync.DefaultBranch); err == nil && ok {
		status.LastRunURL = latest.HTMLURL
		status.LastRunStatus = latest.Status
		status.LastRunConclusion = latest.Conclusion
	}
	return status
}

func runSummary(s repoStatus) string {
	switch {
	case s.LastRunStatus == "gone":
		return "repo gone"
	case s.LastRunURL == "":
		return "none yet"
	case s.LastRunConclusion != "":
		return s.LastRunConclusion
	default:
		return s.LastRunStatus
	}
}

func firstArg(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return "."
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

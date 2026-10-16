package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Entry records one Target repo this machine created. It doubles as a
// configuration source, so an update re-renders the same workflow without the
// user repeating every flag.
type Entry struct {
	Owner        string   `json:"owner"`
	Repo         string   `json:"repo"`
	Workspace    string   `json:"workspace"`
	Image        string   `json:"image"`
	Dockerfile   string   `json:"dockerfile"`
	Tags         []string `json:"tags,omitempty"`
	Workflow     string   `json:"workflow"`
	LastSyncSHA  string   `json:"last_sync_sha"`
	LastSyncAt   string   `json:"last_sync_at"`
	WorkspaceSHA string   `json:"workspace_sha"`
}

// Settings turns the record back into a configuration source.
func (e Entry) Settings() Settings {
	return Settings{
		Workspace:  e.Workspace,
		Owner:      e.Owner,
		Repo:       e.Repo,
		Image:      e.Image,
		Dockerfile: e.Dockerfile,
		Tags:       e.Tags,
		Workflow:   e.Workflow,
	}
}

// State is the local record of every Target repo this machine created.
type State struct {
	path  string
	Repos map[string]Entry `json:"repos"`
}

// LoadState reads the State file, which may not exist yet.
func LoadState(path string) (*State, error) {
	st := &State{path: path, Repos: map[string]Entry{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if st.Repos == nil {
		st.Repos = map[string]Entry{}
	}
	return st, nil
}

func (s *State) Path() string { return s.path }

// Get looks a Target repo up by its "owner/repo" key.
func (s *State) Get(key string) (Entry, bool) {
	e, ok := s.Repos[key]
	return e, ok
}

// ByWorkspace looks a Target repo up by the Workspace directory it mirrors.
func (s *State) ByWorkspace(dir string) (string, Entry, bool) {
	dir = filepath.Clean(dir)
	for key, e := range s.Repos {
		if filepath.Clean(e.Workspace) == dir {
			return key, e, true
		}
	}
	return "", Entry{}, false
}

// Entries returns every record, ordered by key.
func (s *State) Entries() []Entry {
	keys := make([]string, 0, len(s.Repos))
	for k := range s.Repos {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Entry, 0, len(keys))
	for _, k := range keys {
		out = append(out, s.Repos[k])
	}
	return out
}

func (s *State) Put(key string, e Entry) { s.Repos[key] = e }

func (s *State) Remove(key string) { delete(s.Repos, key) }

// Save writes the State file atomically, readable only by its owner.
func (s *State) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

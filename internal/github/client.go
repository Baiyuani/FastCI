// Package github is the slice of the GitHub REST API FastCI needs. It talks
// HTTP only; git operations live elsewhere and reuse the same PAT.
package github

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/crypto/nacl/box"
)

const DefaultBaseURL = "https://api.github.com"

// ErrNotFound reports a 404 from the API.
var ErrNotFound = errors.New("not found")

// Error is a non-2xx API response.
type Error struct {
	Status  int
	Method  string
	Path    string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.Path, e.Status)
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.Status, e.Message)
}

func (e *Error) Is(target error) bool { return target == ErrNotFound && e.Status == 404 }

// Repo is the subset of a repository FastCI reads.
type Repo struct {
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	CloneURL      string `json:"clone_url"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}

// Run is the subset of a workflow run FastCI reports.
type Run struct {
	Number     int    `json:"run_number"`
	HTMLURL    string `json:"html_url"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Event      string `json:"event"`
	HeadSHA    string `json:"head_sha"`
	StartedAt  string `json:"run_started_at"`
	UpdatedAt  string `json:"updated_at"`
}

// Client calls the GitHub API with one PAT.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client

	login string
}

func New(token, baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Login is the authenticated account, fetched once and cached.
func (c *Client) Login() (string, error) {
	if c.login != "" {
		return c.login, nil
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := c.do(http.MethodGet, "/user", nil, &user); err != nil {
		return "", err
	}
	if user.Login == "" {
		return "", errors.New("GET /user returned no login; is the token valid?")
	}
	c.login = user.Login
	return c.login, nil
}

// GetRepo returns the repository and whether it exists.
func (c *Client) GetRepo(owner, repo string) (Repo, bool, error) {
	var out Repo
	err := c.do(http.MethodGet, "/repos/"+owner+"/"+repo, nil, &out)
	if errors.Is(err, ErrNotFound) {
		return Repo{}, false, nil
	}
	if err != nil {
		return Repo{}, false, err
	}
	return out, true, nil
}

// HasCommits reports whether the repository has any commits. An empty repo
// answers 409, which is what a Target repo looks like between a failed create
// and its first push.
func (c *Client) HasCommits(owner, repo string) (bool, error) {
	var commits []struct {
		SHA string `json:"sha"`
	}
	err := c.do(http.MethodGet, "/repos/"+owner+"/"+repo+"/commits?per_page=1", nil, &commits)
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.Status == 409 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(commits) > 0, nil
}

// CreateRepo creates an empty repository under the user's account or an org.
// auto_init stays false: FastCI pushes the first commit itself.
func (c *Client) CreateRepo(owner, name string, private bool) (Repo, error) {
	login, err := c.Login()
	if err != nil {
		return Repo{}, err
	}
	path := "/user/repos"
	if owner != login {
		path = "/orgs/" + owner + "/repos"
	}
	body := map[string]any{"name": name, "private": private, "auto_init": false}
	var out Repo
	if err := c.do(http.MethodPost, path, body, &out); err != nil {
		return Repo{}, err
	}
	return out, nil
}

// SetDefaultBranch points the repository's default branch at an existing one.
func (c *Client) SetDefaultBranch(owner, repo, branch string) error {
	body := map[string]string{"default_branch": branch}
	return c.do(http.MethodPatch, "/repos/"+owner+"/"+repo, body, nil)
}

// DeleteRepo deletes the repository. Image packages are not touched.
func (c *Client) DeleteRepo(owner, repo string) error {
	return c.do(http.MethodDelete, "/repos/"+owner+"/"+repo, nil, nil)
}

// LatestRun returns the newest workflow run on branch, if there is one.
func (c *Client) LatestRun(owner, repo, branch string) (Run, bool, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/runs?per_page=1&branch=%s", owner, repo, url.QueryEscape(branch))
	var out struct {
		Runs []Run `json:"workflow_runs"`
	}
	if err := c.do(http.MethodGet, path, nil, &out); err != nil {
		return Run{}, false, err
	}
	if len(out.Runs) == 0 {
		return Run{}, false, nil
	}
	return out.Runs[0], true, nil
}

// PutSecret stores an Actions secret, sealing it with the repository's public
// key as the API requires.
func (c *Client) PutSecret(owner, repo, name, value string) error {
	var key struct {
		KeyID string `json:"key_id"`
		Key   string `json:"key"`
	}
	base := "/repos/" + owner + "/" + repo + "/actions/secrets/"
	if err := c.do(http.MethodGet, base+"public-key", nil, &key); err != nil {
		return err
	}
	publicKey, err := base64.StdEncoding.DecodeString(key.Key)
	if err != nil {
		return fmt.Errorf("decoding the repository public key: %w", err)
	}
	sealed, err := seal(publicKey, value)
	if err != nil {
		return err
	}
	body := map[string]string{
		"encrypted_value": base64.StdEncoding.EncodeToString(sealed),
		"key_id":          key.KeyID,
	}
	return c.do(http.MethodPut, base+name, body, nil)
}

// seal encrypts value with libsodium's sealed box, which is what the secrets
// API accepts.
func seal(publicKey []byte, value string) ([]byte, error) {
	if len(publicKey) != 32 {
		return nil, fmt.Errorf("repository public key is %d bytes, want 32", len(publicKey))
	}
	var key [32]byte
	copy(key[:], publicKey)
	return box.SealAnonymous(nil, []byte(value), &key, rand.Reader)
}

func (c *Client) do(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Error{Status: resp.StatusCode, Method: method, Path: path, Message: message(data)}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return nil
}

func message(data []byte) string {
	var body struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return ""
	}
	msg := body.Message
	for _, e := range body.Errors {
		if e.Message != "" {
			msg += ": " + e.Message
		}
	}
	return msg
}

package sync

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// git runs git in dir. git's own message carries into the error, because that
// is where the useful part usually is.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

func gitTrim(dir string, args ...string) (string, error) {
	out, err := git(dir, args...)
	return strings.TrimSpace(out), err
}

// authArgs authenticates an HTTPS remote for one invocation. The token travels
// as a config value rather than in the URL, so it never lands in .git/config or
// in a remote's recorded URL.
func authArgs(token string) []string {
	args := []string{"-c", "credential.helper="}
	if token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		args = append(args, "-c", "http.extraheader=AUTHORIZATION: basic "+basic)
	}
	return args
}

// fetchBranch reads branch from url and returns its commit. It deliberately
// leaves the local branch untouched: git refuses to fetch into the branch HEAD
// already points at.
func fetchBranch(dir, url, token, branch string) (string, error) {
	if _, err := git(dir, append(authArgs(token), "fetch", "--quiet", "--no-tags", url, branch)...); err != nil {
		return "", err
	}
	return gitTrim(dir, "rev-parse", "FETCH_HEAD")
}

func pushBranch(dir, url, token, branch string) error {
	_, err := git(dir, append(authArgs(token), "push", "--quiet", url, branch+":"+branch)...)
	return err
}

// indexMatchesHead reports whether committing now would change anything.
func indexMatchesHead(dir string) (bool, error) {
	err := exec.Command("git", "-C", dir, "diff", "--cached", "--quiet").Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

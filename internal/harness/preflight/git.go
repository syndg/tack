package preflight

import (
	"fmt"
	"net/url"
	"strings"
)

func parseGitRemote(remote string) (owner, repo, host string, err error) {
	remote = strings.TrimSpace(remote)
	if strings.HasPrefix(remote, "git@") {
		parts := strings.SplitN(strings.TrimPrefix(remote, "git@"), ":", 2)
		if len(parts) != 2 {
			return "", "", "", fmt.Errorf("unsupported git remote: %s", remote)
		}
		host = parts[0]
		path := strings.TrimSuffix(strings.TrimPrefix(parts[1], "/"), ".git")
		segments := strings.Split(path, "/")
		if len(segments) < 2 {
			return "", "", "", fmt.Errorf("unsupported git remote path: %s", remote)
		}
		return segments[0], segments[1], host, nil
	}

	u, parseErr := url.Parse(remote)
	if parseErr != nil {
		return "", "", "", parseErr
	}
	host = u.Hostname()
	path := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
	segments := strings.Split(path, "/")
	if len(segments) < 2 || host == "" {
		return "", "", "", fmt.Errorf("unsupported git remote path: %s", remote)
	}
	return segments[0], segments[1], host, nil
}

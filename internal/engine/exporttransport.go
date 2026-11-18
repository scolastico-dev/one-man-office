package engine

import (
	"net/url"

	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

// exportSSHUser keeps the login that URI display redaction removes. Only
// SSH uses this transport identity; passwords and HTTPS credentials are
// never copied into the push record.
func exportSSHUser(remote gitops.Remote) string {
	if gitops.ValidSourceURL(remote.URL) != nil {
		return ""
	}
	u, err := url.Parse(remote.URL)
	if err != nil || u.Scheme != "ssh" || u.User == nil {
		return ""
	}
	return u.User.Username()
}

// pushRemote restores the pinned SSH URI login without restoring any
// password. SCP addresses already carry their login; local paths and other
// transports keep the exact recorded address, including literal spaces.
func pushRemote(r pushRecord) gitops.Remote {
	remote := gitops.Remote{Name: r.Remote, URL: r.Address}
	u, err := url.Parse(r.Address)
	if err == nil && u.Scheme == "ssh" && r.SSHUser != "" {
		u.User = url.User(r.SSHUser)
		remote.URL = u.String()
	}
	return remote
}

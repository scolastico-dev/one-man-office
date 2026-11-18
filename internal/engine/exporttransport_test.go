package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

// Losing an SSH URI's username switches Git to the operating-system login
// when the named remote is no longer available. HTTPS credentials, URI
// passwords and non-URI transports must never become an SSHUser pin.
func TestExportSSHUser(t *testing.T) {
	for _, c := range []struct{ url, want string }{
		{"ssh://git@forge.example/org/repo.git", "git"},
		{"ssh://alice:SUPERSECRET@forge.example:2222/org/repo.git", "alice"},
		{"ssh://g%69t@forge.example/org/repo.git", "git"},
		{"ssh://forge.example/org/repo.git", ""},
		{"https://alice:SUPERSECRET@forge.example/org/repo.git", ""},
		{"git@forge.example:org/repo.git", ""},
		{"/tmp/repo with spaces.git", ""},
		{"ssh://-option@forge.example/org/repo.git", ""},
		{"ssh://forge.example/%broken", ""},
		{"ext::ssh malicious", ""},
		{"", ""},
	} {
		t.Run(c.url, func(t *testing.T) {
			if got := exportSSHUser(gitops.Remote{Name: "origin", URL: c.url}); got != c.want {
				t.Fatalf("exportSSHUser = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPushRemotePreservesPinnedTransport(t *testing.T) {
	for _, c := range []struct{ address, user, want string }{
		{"ssh://forge.example:2222/org/repo.git", "git", "ssh://git@forge.example:2222/org/repo.git"},
		{"ssh://forge.example/org/repo.git", "domain\\alice", "ssh://domain%5Calice@forge.example/org/repo.git"},
		{"ssh://forge.example/org/repo.git", "", "ssh://forge.example/org/repo.git"},
		{"git@forge.example:org/repo.git", "", "git@forge.example:org/repo.git"},
		{"/tmp/repo with spaces.git", "", "/tmp/repo with spaces.git"},
		{"https://forge.example/org/repo.git", "git", "https://forge.example/org/repo.git"},
	} {
		t.Run(c.address, func(t *testing.T) {
			got := pushRemote(pushRecord{Remote: "renamed", Address: c.address, SSHUser: c.user})
			if got != (gitops.Remote{Name: "renamed", URL: c.want}) {
				t.Fatalf("pushRemote = %+v, want remote renamed at %q", got, c.want)
			}
		})
	}
}

func TestExportSSHTransportNeverRetainsPassword(t *testing.T) {
	r := gitops.Remote{Name: "origin", URL: "ssh://git:SUPERSECRET@forge.example/org/repo.git"}
	pinned := pushRecord{Remote: r.Name, Address: r.Address(), SSHUser: exportSSHUser(r)}
	data, err := json.Marshal(pinned)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SUPERSECRET") {
		t.Fatalf("transport pin retained password: %s", data)
	}
	got := pushRemote(pinned)
	if got.URL != "ssh://git@forge.example/org/repo.git" {
		t.Fatalf("restored transport = %q, want SSH username without password", got.URL)
	}
	if strings.Contains(got.Address(), "git@") || strings.Contains(got.Address(), "SUPERSECRET") {
		t.Fatalf("display address exposed URI userinfo: %q", got.Address())
	}
}

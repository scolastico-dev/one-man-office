package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Bounds of a client ID metadata document fetch.
const (
	fetchTimeout   = 5 * time.Second
	maxDocument    = 64 << 10
	maxDocumentURL = 2048
	documentTTL    = 10 * time.Minute
	maxDocuments   = 64
	maxRedirectsIn = 3
)

// cachedDocument is a client read from its metadata document.
type cachedDocument struct {
	client  client
	fetched time.Time
}

// document resolves a client whose id is the https address of its client
// ID metadata document. Documents are cached for ten minutes.
func (a *AS) document(ctx context.Context, id string) (client, error) {
	now := a.store.clock()
	a.mu.Lock()
	if d, ok := a.docs[id]; ok && now.Sub(d.fetched) < documentTTL {
		a.mu.Unlock()
		return d.client, nil
	}
	a.mu.Unlock()
	c, err := a.fetchDocument(ctx, id)
	if err != nil {
		return client{}, api.Errorf(api.CodeBadRequest, "the client's metadata document cannot be used: %s", err.Error())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.docs) >= maxDocuments {
		for k, d := range a.docs {
			if now.Sub(d.fetched) >= documentTTL {
				delete(a.docs, k)
			}
		}
		if len(a.docs) >= maxDocuments {
			a.docs = map[string]cachedDocument{}
		}
	}
	a.docs[id] = cachedDocument{client: c, fetched: now}
	return c, nil
}

// fetchDocument reads and checks a client ID metadata document. Its errors
// are short reasons shown on the consent page.
func (a *AS) fetchDocument(ctx context.Context, id string) (client, error) {
	u, err := url.Parse(id)
	switch {
	case len(id) > maxDocumentURL:
		return client{}, errors.New("its address is too long")
	case err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "":
		return client{}, errors.New("its address must be an https URL")
	case u.Path == "" || u.Path == "/":
		return client{}, errors.New("its address must have a path")
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	if err != nil {
		return client{}, errors.New("its address is malformed")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.fetch.Do(req)
	if err != nil {
		return client{}, fmt.Errorf("it could not be fetched (%s)", fetchReason(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return client{}, fmt.Errorf("its server answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocument+1))
	if err != nil {
		return client{}, errors.New("it could not be read")
	}
	if len(body) > maxDocument {
		return client{}, fmt.Errorf("it is larger than %d KiB", maxDocument>>10)
	}
	var doc struct {
		ClientID                string   `json:"client_id"`
		ClientName              string   `json:"client_name"`
		RedirectURIs            []string `json:"redirect_uris"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return client{}, errors.New("it is not a JSON object")
	}
	name := cleanName(doc.ClientName)
	switch {
	case doc.ClientID != id:
		return client{}, errors.New("its client_id is not its own address")
	case name == "":
		return client{}, errors.New("it names no client_name")
	case doc.TokenEndpointAuthMethod != "" && doc.TokenEndpointAuthMethod != "none":
		return client{}, errors.New("omo accepts public clients only (token_endpoint_auth_method none)")
	case len(doc.RedirectURIs) == 0 || len(doc.RedirectURIs) > maxRedirects:
		return client{}, fmt.Errorf("it must list 1 to %d redirect_uris", maxRedirects)
	}
	for _, r := range doc.RedirectURIs {
		if len(r) > maxRedirectLength || !validRedirect(r) {
			return client{}, errors.New("a redirect URI is neither loopback http nor https")
		}
	}
	return client{ID: id, Name: name, Kind: api.OAuthClientCIMD, Redirects: doc.RedirectURIs}, nil
}

// fetchReason is a short reason for a failed fetch, without the address.
func fetchReason(err error) string {
	var refused errRefusedAddress
	switch {
	case errors.As(err, &refused):
		return refused.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, errRedirect):
		return errRedirect.Error()
	}
	return "connection failed"
}

// errRefusedAddress refuses a connection to an address that is not public.
type errRefusedAddress struct{ addr string }

func (e errRefusedAddress) Error() string {
	return "the address " + e.addr + " is not public"
}

var errRedirect = errors.New("it redirects to an address that is not https, or too often")

// SafeClient is the client that fetches client ID metadata documents: https
// only (redirects too, at most three), five seconds, no proxy, and it never
// connects to loopback, private, link-local, multicast or unspecified
// addresses. The address is checked when the connection is made, after
// name resolution, so a name that resolves to such an address is refused
// too.
func SafeClient() *http.Client {
	dialer := &net.Dialer{Timeout: fetchTimeout, Control: refuseNonPublic}
	return &http.Client{
		Timeout: fetchTimeout,
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            dialer.DialContext,
			TLSHandshakeTimeout:    fetchTimeout,
			ResponseHeaderTimeout:  fetchTimeout,
			MaxResponseHeaderBytes: 16 << 10,
			ForceAttemptHTTP2:      true,
		},
		CheckRedirect: checkRedirect,
	}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	req.Header.Del("Referer")
	if req.URL.Scheme != "https" || len(via) > maxRedirectsIn {
		return errRedirect
	}
	return nil
}

// PublicOnly is a dialer's Control that refuses every address that is not
// public (loopback, private, link-local, multicast, unspecified, shared),
// as SafeClient does: it runs after name resolution, for the address
// actually connected to.
func PublicOnly(network, address string, c syscall.RawConn) error {
	return refuseNonPublic(network, address, c)
}

// refuseNonPublic is the dialer's Control: it runs after name resolution,
// for the address actually connected to.
func refuseNonPublic(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errRefusedAddress{addr: address}
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !publicAddr(ip) {
		return errRefusedAddress{addr: host}
	}
	return nil
}

// sharedSpace is 100.64.0.0/10, the carrier-grade NAT range.
var sharedSpace = netip.MustParsePrefix("100.64.0.0/10")

// publicAddr reports whether ip may be connected to for a document.
func publicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	switch {
	case !ip.IsValid(), ip.IsUnspecified(), ip.IsLoopback(), ip.IsPrivate(),
		ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return false
	case ip.Is4() && (ip.As4()[0] == 0 || sharedSpace.Contains(ip) || ip == netip.AddrFrom4([4]byte{255, 255, 255, 255})):
		return false
	}
	return true
}

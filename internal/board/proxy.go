package board

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/netip"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

const proxySelect = `SELECT f.id, f.ticket_id, f.host, f.port, f.scheme, f.name,
	f.start_path, f.strip_prefix, f.verify_tls FROM proxy_forwards f`

func scanProxyForward(row interface{ Scan(...any) error }) (api.ProxyForward, error) {
	var f api.ProxyForward
	if err := row.Scan(&f.ID, &f.Ticket, &f.Host, &f.Port, &f.Scheme, &f.Name, &f.StartPath, &f.StripPrefix, &f.VerifyTLS); err != nil {
		return f, err
	}
	f.BasePath = "/proxy/" + f.ID + "/"
	f.Address = f.BasePath + strings.TrimPrefix(f.StartPath, "/")
	return f, nil
}

func proxyNotFound() error { return api.Errorf(api.CodeNotFound, "proxy forward does not exist") }

func loadProxyForward(q db.Queryer, id string) (api.ProxyForward, error) {
	f, err := scanProxyForward(q.QueryRow(proxySelect+` WHERE f.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return f, proxyNotFound()
	}
	return f, err
}

func proxyTicket(f api.ProxyForward) int64 {
	if f.Ticket == nil {
		return 0
	}
	return int64(*f.Ticket)
}

// checkProxyWrite runs before looking up a ticket, so neither its existence
// nor its closed state leaks to an agent that cannot manage its forwards.
func checkProxyWrite(a Actor, ticket *int) error {
	if a.Role == api.RoleUser {
		return nil
	}
	if a.isAgent() && a.Ticket > 0 && ticket != nil && int64(*ticket) == a.Ticket {
		return nil
	}
	return api.Errorf(api.CodeForbidden, "only the user manages global forwards; agents manage only their own ticket's forwards")
}

func canReadProxy(q db.Queryer, a Actor, f api.ProxyForward) (bool, error) {
	if f.Ticket != nil {
		return canRead(q, a, int64(*f.Ticket))
	}
	_, _, all := readScope(a)
	return all, nil
}

func checkProxyInput(req api.CreateProxyForward) (api.CreateProxyForward, error) {
	bad := func(message string) (api.CreateProxyForward, error) {
		return req, api.Errorf(api.CodeValidation, "%s", message)
	}
	if req.Ticket != nil && *req.Ticket <= 0 {
		return bad("ticket must be a positive number or null for global")
	}
	if req.Port < 1 || req.Port > 65535 {
		return bad("port must be between 1 and 65535")
	}
	if !utf8.ValidString(req.Name) || strings.ContainsFunc(req.Name, isTitleControl) {
		return bad("name must be one line without hidden or control characters")
	}
	req.Name = strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(req.Name); n < 1 || n > 200 {
		return bad("name must have 1 to 200 characters")
	}
	if req.Host == "" {
		req.Host = "127.0.0.1"
	}
	if strings.HasPrefix(req.Host, "[") && strings.HasSuffix(req.Host, "]") {
		inner := req.Host[1 : len(req.Host)-1]
		addr, err := netip.ParseAddr(inner)
		if err != nil || !addr.Is6() {
			return bad("host must be a host name or IP address without a port")
		}
		req.Host = inner
	}
	if !validProxyHost(req.Host) {
		return bad("host must be a host name or IP address without a scheme, port, path or credentials")
	}
	if req.Scheme == "" {
		req.Scheme = "http"
	}
	if req.Scheme != "http" && req.Scheme != "https" {
		return bad("scheme must be http or https")
	}
	if req.StartPath == "" {
		req.StartPath = "/"
	}
	if !validProxyStartPath(req.StartPath) {
		return bad("start_path must stay inside the forward: a / path and optional query, without a host, fragment, controls or escaping traversal")
	}
	return req, nil
}

func validProxyHost(host string) bool {
	if !utf8.ValidString(host) || len(strings.TrimSuffix(host, ".")) > 253 || strings.ContainsFunc(host, func(r rune) bool { return unicode.IsSpace(r) || isTitleControl(r) }) || strings.ContainsAny(host, "/\\?#@[]") {
		return false
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return !strings.ContainsAny(addr.Zone(), "%:")
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if !unicode.IsLetter(r) && !unicode.IsMark(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
				return false
			}
		}
	}
	return true
}

// Inspect decoded path segments, including nested percent escapes, since
// browsers and upstream routers may decode or normalize before routing.
// Queries are checked for hidden characters separately: a URL or ../ in a
// query is data and cannot escape the forward's base path.
func validProxyStartPath(raw string) bool {
	if len(raw) > 8192 || !utf8.ValidString(raw) || strings.ContainsFunc(raw, isTitleControl) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.Opaque != "" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") {
		return false
	}
	query, err := url.QueryUnescape(u.RawQuery)
	if err != nil || !utf8.ValidString(query) || strings.ContainsFunc(query, isTitleControl) {
		return false
	}
	path := u.EscapedPath()
	for {
		if !validProxyDecodedPath(path) {
			return false
		}
		decoded := decodeProxyPath(path)
		if decoded == path {
			return true
		}
		path = decoded
	}
}

// Decode each valid escape independently. A literal percent must not mask
// later nested escapes when an upstream performs another decoding pass.
func decodeProxyPath(path string) string {
	var out strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] == '%' && i+2 < len(path) {
			if decoded, err := url.PathUnescape(path[i : i+3]); err == nil {
				out.WriteString(decoded)
				i += 2
				continue
			}
		}
		out.WriteByte(path[i])
	}
	return out.String()
}

func validProxyDecodedPath(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "\\") || !utf8.ValidString(path) || strings.ContainsFunc(path, isTitleControl) {
		return false
	}
	depth := 0
	for _, part := range strings.Split(path, "/") {
		switch part {
		case "", ".":
		case "..":
			depth--
			if depth < 0 {
				return false
			}
		default:
			depth++
		}
	}
	return true
}

// CreateProxyForward persists a target without contacting or starting it.
// Agents may register only for their own ticket; globals are user-only.
func (b *Board) CreateProxyForward(ctx context.Context, a Actor, req api.CreateProxyForward) (api.ProxyForward, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return api.ProxyForward{}, err
	}
	if err := checkProxyWrite(a, req.Ticket); err != nil {
		return api.ProxyForward{}, err
	}
	req, err := checkProxyInput(req)
	if err != nil {
		return api.ProxyForward{}, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return api.ProxyForward{}, err
	}
	id := hex.EncodeToString(random[:])
	var out api.ProxyForward
	err = b.write(ctx, func(tx *sql.Tx) error {
		if req.Ticket != nil {
			t, err := loadTicket(tx, int64(*req.Ticket))
			if err != nil {
				return err
			}
			if t.Col == api.ColumnDone {
				return api.Errorf(api.CodeInvalidTransition, "cannot add a forward to a closed ticket")
			}
		}
		if _, err := tx.Exec(`INSERT INTO proxy_forwards(id,ticket_id,host,port,scheme,name,start_path,strip_prefix,verify_tls) VALUES(?,?,?,?,?,?,?,?,?)`, id, req.Ticket, req.Host, req.Port, req.Scheme, req.Name, req.StartPath, req.StripPrefix, req.VerifyTLS); err != nil {
			return err
		}
		var err error
		out, err = loadProxyForward(tx, id)
		if err != nil {
			return err
		}
		return emit(tx, api.KindProxyForwardAdded, proxyTicket(out), a, out)
	})
	return forActor(a, out, err)
}

// ProxyForwards lists all forwards in the actor's ticket read scope. A
// ticket filter narrows that scope; it never grants access. Globals are
// visible only to actors with unrestricted ticket reads.
func (b *Board) ProxyForwards(ctx context.Context, a Actor, ticket *int) ([]api.ProxyForward, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return nil, err
	}
	out := []api.ProxyForward{}
	err := b.read(ctx, func(tx *sql.Tx) error {
		if ticket != nil {
			if err := checkRead(tx, a, int64(*ticket)); err != nil {
				return err
			}
			if _, err := loadTicket(tx, int64(*ticket)); err != nil {
				return err
			}
		}
		cond, args, all := readScope(a)
		query := proxySelect + ` WHERE 1=1`
		if !all {
			query += ` AND f.ticket_id IN (SELECT t.id FROM tickets t WHERE ` + cond + `)`
		} else {
			args = nil
		}
		if ticket != nil {
			query += ` AND f.ticket_id = ?`
			args = append(args, *ticket)
		}
		rows, err := tx.Query(query+` ORDER BY f.id`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f, err := scanProxyForward(rows)
			if err != nil {
				return err
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	return forActor(a, out, err)
}

// ProxyForward looks up one record within the actor's read scope. Missing
// and unreadable ids return the same error.
func (b *Board) ProxyForward(ctx context.Context, a Actor, id string) (api.ProxyForward, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return api.ProxyForward{}, err
	}
	var out api.ProxyForward
	err := b.read(ctx, func(tx *sql.Tx) error {
		f, err := loadProxyForward(tx, id)
		if err != nil {
			return err
		}
		ok, err := canReadProxy(tx, a, f)
		if err != nil {
			return err
		}
		if !ok {
			return proxyNotFound()
		}
		out = f
		return nil
	})
	return forActor(a, out, err)
}

// LiveProxyForward is the server-internal routing lookup. It requires the
// system actor and returns only a still-live global or open-ticket forward.
func (b *Board) LiveProxyForward(ctx context.Context, a Actor, id string) (api.ProxyForward, error) {
	if a.Role != api.RoleSystem {
		return api.ProxyForward{}, api.Errorf(api.CodeForbidden, "only omo routes proxy traffic")
	}
	var out api.ProxyForward
	err := b.read(ctx, func(tx *sql.Tx) error {
		f, err := loadProxyForward(tx, id)
		if err != nil {
			return err
		}
		if f.Ticket != nil {
			t, err := loadTicket(tx, int64(*f.Ticket))
			if err != nil {
				return err
			}
			if t.Col == api.ColumnDone {
				return proxyNotFound()
			}
		}
		out = f
		return nil
	})
	return out, err
}

func removeProxyForward(tx *sql.Tx, a Actor, f api.ProxyForward) error {
	if _, err := tx.Exec(`DELETE FROM proxy_forwards WHERE id = ?`, f.ID); err != nil {
		return err
	}
	return emit(tx, api.KindProxyForwardRemoved, proxyTicket(f), a, f)
}

// DeleteProxyForward removes a record and emits its removal atomically.
func (b *Board) DeleteProxyForward(ctx context.Context, a Actor, id string) error {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return err
	}
	return b.write(ctx, func(tx *sql.Tx) error {
		f, err := loadProxyForward(tx, id)
		if err != nil {
			return err
		}
		ok, err := canReadProxy(tx, a, f)
		if err != nil {
			return err
		}
		if !ok {
			return proxyNotFound()
		}
		if err := checkProxyWrite(a, f.Ticket); err != nil {
			return err
		}
		return removeProxyForward(tx, a, f)
	})
}

// closeProxyForwards runs inside setColumn's transaction for every Done
// transition, including cancellations cascading from a story.
func closeProxyForwards(tx *sql.Tx, n int64) error {
	var forwards []api.ProxyForward
	if err := each(tx, proxySelect+` WHERE f.ticket_id = ? ORDER BY f.id`, n, func(rows *sql.Rows) error {
		f, err := scanProxyForward(rows)
		if err != nil {
			return err
		}
		forwards = append(forwards, f)
		return nil
	}); err != nil {
		return err
	}
	for _, f := range forwards {
		if err := removeProxyForward(tx, System, f); err != nil {
			return err
		}
	}
	return nil
}

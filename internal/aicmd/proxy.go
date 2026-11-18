package aicmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

type proxyAddIn struct {
	Ticket      *int   `json:"ticket,omitempty" flag:"ticket" help:"ticket number (default: your own ticket; global for the user)" jsonschema:"the ticket number; omit for your own ticket, or a global forward as the user"`
	Port        int    `json:"port" flag:"port" required:"true" help:"target port (1-65535)" jsonschema:"the target port, 1 to 65535"`
	Name        string `json:"name" flag:"name" required:"true" help:"display name of the web UI" jsonschema:"the display name of the web UI"`
	Host        string `json:"host,omitempty" flag:"host" default:"127.0.0.1" help:"target host name or IP address, including IPv6" jsonschema:"target host name or IP address, including IPv6; default 127.0.0.1"`
	Scheme      string `json:"scheme,omitempty" flag:"scheme" default:"http" help:"target scheme: http or https" jsonschema:"target scheme, http or https; default http"`
	StartPath   string `json:"start_path,omitempty" flag:"start-path" default:"/" help:"start path within the forward, optionally with a query" jsonschema:"start path within the forward, optionally with a query; default /"`
	StripPrefix bool   `json:"strip_prefix,omitempty" flag:"strip-prefix" help:"strip the proxy prefix for apps using relative addresses" jsonschema:"strip the proxy prefix for apps using relative addresses; default false"`
	VerifyTLS   bool   `json:"verify_tls,omitempty" flag:"verify-tls" help:"verify an HTTPS target's certificate (default: accept self-signed)" jsonschema:"verify an HTTPS target's certificate; default false accepts self-signed certificates"`
}

type proxyListIn struct {
	Ticket *int `json:"ticket,omitempty" flag:"ticket" help:"ticket number (default: your own ticket; all readable forwards for the user)" jsonschema:"filter by ticket; omit for your own ticket, or all readable forwards as the user"`
}

type proxyRemoveIn struct {
	ID string `json:"id" arg:"0" jsonschema:"the forward id to remove"`
}

func checkProxyTicket(ticket *int) error {
	if ticket != nil && *ticket <= 0 {
		return fmt.Errorf("ticket must be a positive number")
	}
	return nil
}

func (in proxyAddIn) check() error  { return checkProxyTicket(in.Ticket) }
func (in proxyListIn) check() error { return checkProxyTicket(in.Ticket) }

func proxyCommands() []Command {
	return []Command{
		Def("proxy add", "Register a web UI forward for your own ticket (global for the user), returning address and base_path; start the target separately", everyone, proxyAdd).help("add --port N --name NAME",
			`Register a web UI that someone else runs on the omo host or another host.
Agents register only for their own ticket; --ticket defaults to that ticket.
As the user, omit --ticket for a global forward. The target process is not
started by omo. Register first, then configure path-mode apps with the printed
base_path. With --strip-prefix the upstream receives paths without that prefix;
use it for relative-address apps. Subdomain addresses always send unprefixed
paths: configure the app at /. Path URLs remain available. HTTPS targets accept
self-signed certificates by default; --verify-tls enables certificate checks.
Registration never probes the target.
Ticket forwards are removed when the ticket is done or cancelled.`,
			`  omo ai proxy add --port 5173 --name "dev server"`),
		Def("proxy list", "List web UI forwards within your ticket read scope", everyone, proxyList).help("list [--ticket N]",
			`List forwards for your own ticket, or --ticket N for another ticket you
may read. As the user, omitting --ticket lists all forwards, including globals.
Targets are not probed, so an offline web UI remains listed.`,
			"  omo ai proxy list"),
		Def("proxy remove", "Remove a web UI forward by id", everyone, proxyRemove).help("remove ID",
			`Remove a forward. Agents remove only forwards of their own ticket;
the user may remove any forward, including globals. This does not stop the
target process.`,
			"  omo ai proxy remove FORWARD_ID"),
	}
}

func proxyTicketSelection(ctx context.Context, c *apiclient.Client, ticket *int) (*int, error) {
	if ticket != nil {
		return ticket, nil
	}
	me, err := c.Me(ctx)
	if err != nil {
		return nil, err
	}
	return me.Ticket, nil
}

func printProxyForward(w io.Writer, f api.ProxyForward) {
	owner := "global"
	if f.Ticket != nil {
		owner = "#" + strconv.Itoa(*f.Ticket)
	}
	fmt.Fprintf(w, "forward %s (%s): %s\n", OneLine(f.ID), owner, OneLine(f.Name))
	fmt.Fprintf(w, "target: %s://%s\n", OneLine(f.Scheme), OneLine(net.JoinHostPort(f.Host, strconv.Itoa(f.Port))))
	fmt.Fprintf(w, "address: %s\nbase_path: %s\n", OneLine(f.Address), OneLine(f.BasePath))
}

func proxyAdd(ctx context.Context, c *apiclient.Client, in proxyAddIn) (Result, error) {
	ticket, err := proxyTicketSelection(ctx, c, in.Ticket)
	if err != nil {
		return Result{}, err
	}
	f, err := c.CreateProxyForward(ctx, api.CreateProxyForward{Ticket: ticket, Host: in.Host, Port: in.Port, Scheme: in.Scheme, Name: in.Name, StartPath: in.StartPath, StripPrefix: in.StripPrefix, VerifyTLS: in.VerifyTLS})
	if err != nil {
		return Result{}, err
	}
	return last(c, textOf(func(w *strings.Builder) {
		printProxyForward(w, f)
		if f.StripPrefix {
			fmt.Fprintln(w, "Path mode: strip_prefix is enabled; serve unprefixed paths and use relative app addresses.")
		} else {
			fmt.Fprintln(w, "Path mode: start the app with the required base_path above (prefix kept).")
		}
		fmt.Fprintln(w, "Subdomain mode: serve at /; no path prefix is sent. Path URLs remain available.")
		fmt.Fprintln(w, "The target process is not started by omo. Name this forward in your hand-in.")
		if f.Ticket != nil {
			fmt.Fprintln(w, "This forward is removed when its owning ticket is done or cancelled.")
		} else {
			fmt.Fprintln(w, "This global forward remains until the user removes it.")
		}
	})), nil
}

func proxyList(ctx context.Context, c *apiclient.Client, in proxyListIn) (Result, error) {
	ticket, err := proxyTicketSelection(ctx, c, in.Ticket)
	if err != nil {
		return Result{}, err
	}
	list, err := c.ProxyForwards(ctx, ticket)
	if err != nil {
		return Result{}, err
	}
	return last(c, textOf(func(w *strings.Builder) {
		if len(list.Forwards) == 0 {
			fmt.Fprintln(w, "no forwards")
		}
		for _, f := range list.Forwards {
			printProxyForward(w, f)
		}
	})), nil
}

func proxyRemove(ctx context.Context, c *apiclient.Client, in proxyRemoveIn) (Result, error) {
	if err := c.DeleteProxyForward(ctx, in.ID); err != nil {
		return Result{}, err
	}
	return last(c, "forward "+OneLine(in.ID)+" removed\n"), nil
}

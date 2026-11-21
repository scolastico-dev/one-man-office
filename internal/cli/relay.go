package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/spf13/cobra"
)

var newRelayClient = apiclient.FromEnvironment

func addRelayCommand(root *cobra.Command) {
	relay := &cobra.Command{Use: "relay", Short: "Connect to a relay and manage paired devices from the local host"}
	var clientID string
	var newID, passwordStdin bool
	connect := &cobra.Command{Use: "connect ORIGIN", Short: "Save and enable the outbound relay connection", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		req := api.RelayConnectRequest{Origin: args[0], ClientID: clientID, NewID: newID}
		if passwordStdin {
			raw, e := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4098))
			if e != nil {
				return errors.New("could not read relay password")
			}
			if len(raw) > 4097 {
				return errors.New("relay password exceeds 4096 bytes")
			}
			pass := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
			if len(pass) > 4096 {
				return errors.New("relay password exceeds 4096 bytes")
			}
			req.Password = &pass
		}
		c, e := newRelayClient()
		if e != nil {
			return e
		}
		status, e := c.RelayConnect(cmd.Context(), req)
		if e != nil {
			return e
		}
		printRelayStatus(cmd, status)
		return nil
	}}
	connect.Flags().StringVar(&clientID, "client-id", "", "Persistent routing name (otherwise reuse or generate)")
	connect.Flags().BoolVar(&newID, "new-id", false, "Explicitly generate a new routing name, keeping the host key")
	connect.Flags().BoolVar(&passwordStdin, "password-stdin", false, "Read relay admission password from standard input")
	connect.MarkFlagsMutuallyExclusive("client-id", "new-id")
	status := &cobra.Command{Use: "status", Short: "Show connection, route and host fingerprint", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := newRelayClient()
		if e != nil {
			return e
		}
		status, e := c.RelayStatus(cmd.Context())
		if e != nil {
			return e
		}
		printRelayStatus(cmd, status)
		return nil
	}}
	disconnect := &cobra.Command{Use: "disconnect", Short: "Disable reconnection and close live relay work; retain paired devices", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := newRelayClient()
		if e != nil {
			return e
		}
		if e = c.RelayDisconnect(cmd.Context()); e != nil {
			return e
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Relay disabled; paired devices retained.")
		return nil
	}}
	pair := &cobra.Command{Use: "pair", Short: "Authorize one local pairing ceremony"}
	begin := &cobra.Command{Use: "begin", Short: "Open one new five-minute attempt", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := newRelayClient()
		if e != nil {
			return e
		}
		if e = c.RelayPairBegin(cmd.Context()); e != nil {
			return e
		}
		fmt.Fprintln(cmd.OutOrStdout(), "New pairing ceremony opened for one attempt, for five minutes. Previous unfinished attempts are spent.")
		return nil
	}}
	pending := &cobra.Command{Use: "pending", Short: "Show the single pairing attempt (never its PIN)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := newRelayClient()
		if e != nil {
			return e
		}
		p, e := c.RelayPairPending(cmd.Context())
		if e != nil {
			return e
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s\naid: %s\nui_id: %s\nexpires: %s\n", p.Phase, p.AID, p.UIID, p.Expires.UTC().Format("2006-01-02T15:04:05Z"))
		return nil
	}}
	var publicKey, name string
	accept := &cobra.Command{Use: "accept AID [PIN]", Short: "Accept this proved UI using its six-digit PIN or full public key", Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
		pin := ""
		if len(args) == 2 {
			pin = args[1]
		}
		if (len(args) == 2) == cmd.Flags().Changed("public-key") {
			return errors.New("provide either PIN or --public-key")
		}
		c, e := newRelayClient()
		if e != nil {
			return e
		}
		a, e := c.RelayPairAccept(cmd.Context(), api.RelayAcceptRequest{AID: args[0], PIN: pin, PublicKey: publicKey, Name: name})
		if e != nil {
			return e
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Device accepted and active.\naid: %s\nhost_fp: %s\nui_id: %s\n", a.AID, a.HostFP, a.Device.UIID)
		return nil
	}}
	accept.Flags().StringVar(&publicKey, "public-key", "", "Full unpadded base64url ML-KEM-768 public key (not a fingerprint)")
	accept.Flags().StringVar(&name, "name", "", "Local device label")
	cancel := &cobra.Command{Use: "cancel", Short: "Spend the current pairing slot", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := newRelayClient()
		if e != nil {
			return e
		}
		if e = c.RelayPairCancel(cmd.Context()); e != nil {
			return e
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Pairing slot spent.")
		return nil
	}}
	pair.AddCommand(begin, pending, accept, cancel)
	devices := &cobra.Command{Use: "devices", Short: "List paired devices and live connection counts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := newRelayClient()
		if e != nil {
			return e
		}
		list, e := c.RelayDevices(cmd.Context())
		if e != nil {
			return e
		}
		for _, d := range list.Devices {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%q\t%s\t%d live\n", d.UIID, d.State, d.Name, d.AcceptedAt.UTC().Format("2006-01-02T15:04:05Z"), d.LiveConnections)
		}
		if len(list.Devices) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No paired devices.")
		}
		return nil
	}}
	unregister := &cobra.Command{Use: "unregister UI-ID", Short: "Revoke this device, including offline and live access", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, e := newRelayClient()
		if e != nil {
			return e
		}
		if e = c.RelayUnregister(cmd.Context(), args[0]); e != nil {
			return e
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Device revoked; its live relay work has stopped.")
		return nil
	}}
	relay.AddCommand(connect, status, disconnect, pair, devices, unregister)
	root.AddCommand(relay)
}
func printRelayStatus(cmd *cobra.Command, s api.RelayStatus) {
	fmt.Fprintf(cmd.OutOrStdout(), "%s (enabled: %t)\nrelay: %s\nclient_id: %s\nhost_fp: %s\n", s.State, s.Enabled, s.Origin, s.HostID, s.HostFP)
	if s.RetrySeconds > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "retry: %ds\n", s.RetrySeconds)
	}
}

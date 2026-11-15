package cli

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/service"
)

const nextStartNote = "A running omo applies this at the next start."

func addConfigCommand(root *cobra.Command) {
	config := &cobra.Command{Use: "config", Short: "Read and change the server start options"}
	config.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List every option",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, c, err := loadConfig()
				if err != nil {
					return err
				}
				for _, key := range serverconfig.Keys() {
					value := configValue(c, key)
					if key == "basic_auth" && value != "" {
						value = "***"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", key, value)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "get KEY",
			Short: "Print one option",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				_, c, err := loadConfig()
				if err != nil {
					return err
				}
				if !validKey(args[0]) {
					return fmt.Errorf("unknown key %q (valid keys: %s)", args[0], strings.Join(serverconfig.Keys(), ", "))
				}
				fmt.Fprintln(cmd.OutOrStdout(), configValue(c, args[0]))
				return nil
			},
		},
		&cobra.Command{
			Use:   "set KEY VALUE",
			Short: "Change one option",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				layout, _, err := loadConfig()
				if err != nil {
					return err
				}
				if _, err := serverconfig.Set(layout.ServerConfig, args[0], args[1]); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s updated.\n", args[0])
				noteIfRunning(cmd)
				return nil
			},
		},
		&cobra.Command{
			Use:   "unset KEY",
			Short: "Reset one option to its default",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				layout, _, err := loadConfig()
				if err != nil {
					return err
				}
				if _, err := serverconfig.Unset(layout.ServerConfig, args[0]); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s reset to its default.\n", args[0])
				noteIfRunning(cmd)
				return nil
			},
		},
		&cobra.Command{
			Use:   "rotate-token",
			Short: "Replace the login token",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				layout, c, err := loadConfig()
				if err != nil {
					return err
				}
				token, err := serverconfig.RotateToken(layout.Token)
				if err != nil {
					return err
				}
				base := tokenBase(c)
				running := false
				if current, ok, err := service.Running(cmd.Context()); err == nil && ok {
					running = true
					if u, perr := url.Parse(current); perr == nil {
						u.Fragment = ""
						base = u.String()
					}
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Login token replaced.")
				fmt.Fprintf(cmd.OutOrStdout(), "omo: %s#%s\n", base, token)
				if running {
					fmt.Fprintln(cmd.OutOrStdout(), "The running omo keeps the old token. "+nextStartNote)
				}
				return nil
			},
		},
	)
	root.AddCommand(config)
}

func loadConfig() (globalhome.Layout, serverconfig.Config, error) {
	layout, err := globalhome.Open()
	if err != nil {
		return layout, serverconfig.Config{}, err
	}
	c, err := serverconfig.Load(layout.ServerConfig)
	return layout, c, err
}

func noteIfRunning(cmd *cobra.Command) {
	if _, running, err := service.Running(cmd.Context()); err == nil && running {
		fmt.Fprintln(cmd.OutOrStdout(), nextStartNote)
	}
}

func validKey(key string) bool {
	for _, k := range serverconfig.Keys() {
		if k == key {
			return true
		}
	}
	return false
}

func configValue(c serverconfig.Config, key string) string {
	switch key {
	case "host":
		return c.Host
	case "port":
		return strconv.Itoa(c.Port)
	case "auth":
		return c.Auth
	case "basic_auth":
		return c.BasicAuth
	case "origin_check":
		return strconv.FormatBool(c.OriginCheck)
	}
	return ""
}

// tokenBase builds the address a server with these options would print.
func tokenBase(c serverconfig.Config) string {
	host := c.Host
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && (ip.IsLoopback() || ip.IsUnspecified())) {
		host = "omo.localhost"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(c.Port)) + "/"
}

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/aicmd"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

// addBackupCommands adds `omo config export` and `omo config import`. Both
// talk to the running omo, which does the same as the Backup page of the
// settings.
func addBackupCommands(config *cobra.Command) {
	var withDB bool
	export := &cobra.Command{
		Use:   "export FILE",
		Short: "Write omo's settings to a zip file",
		Long: `Write omo's settings to a zip file: the general settings, server.yaml,
projects, agent profiles, roles, prompts, usage scripts, skill sets, plugin
settings and the MCP servers omo relays. With --with-db a consistent copy of
the database (tickets, comments, history) is added.

The file contains your tokens and environment values; keep it private.
omo must be running.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := apiclient.FromEnvironment()
			if err != nil {
				return err
			}
			path := "/api/settings/export"
			if withDB {
				path += "?database=true"
			}
			resp, err := c.Raw(cmd.Context(), http.MethodGet, path, "", nil)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := writeNewFile(args[0], resp.Body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Settings written to %s. The file contains your tokens and environment values; keep it private.\n", args[0])
			return nil
		},
	}
	export.Flags().BoolVar(&withDB, "with-db", false, "also include the database (tickets, comments, history)")

	var yes, server, importDB bool
	imp := &cobra.Command{
		Use:   "import FILE",
		Short: "Check a settings zip and, with --yes, apply it",
		Long: `Check a zip written by omo config export (or the Backup page) and show
which files it replaces. Nothing changes without --yes. Files the zip does
not hold are left alone; the replaced files are kept in a folder below
backups/ in the omo home.

--server also writes server.yaml (start options; they apply at the next
start). --with-db also replaces the database with the zip's, which replaces
all tickets: it is refused while agents run, omo pauses, and the database is
swapped when omo starts again. omo must be running.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := apiclient.FromEnvironment()
			if err != nil {
				return err
			}
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			resp, err := c.Raw(cmd.Context(), http.MethodPost, "/api/settings/import", "application/octet-stream", f)
			if err != nil {
				return err
			}
			var plan api.ImportPlan
			err = json.NewDecoder(resp.Body).Decode(&plan)
			resp.Body.Close()
			if err != nil {
				return fmt.Errorf("read the answer: %w", err)
			}
			out := cmd.OutOrStdout()
			printPlan(out, plan, server, importDB)
			if importDB && !plan.Database {
				return fmt.Errorf("the zip holds no database")
			}
			if !yes {
				fmt.Fprintln(out, "Nothing changed. Run again with --yes to apply.")
				return nil
			}
			var res api.ImportResult
			if err := c.Do(cmd.Context(), http.MethodPost, "/api/settings/import/apply", api.ImportApply{ID: plan.ID, Server: server, Database: importDB}, &res); err != nil {
				return err
			}
			fmt.Fprintf(out, "Imported %d files.\n", len(res.Written))
			if res.Backup != "" {
				fmt.Fprintf(out, "The replaced files are kept in %s.\n", res.Backup)
			}
			if res.DatabasePending {
				fmt.Fprintln(out, "omo is paused. Restart omo (omo stop, then omo) to replace the database; the current one is kept below backups/.")
			} else if res.RestartNeeded {
				fmt.Fprintln(out, "server.yaml applies at the next start of omo.")
			}
			return nil
		},
	}
	imp.Flags().BoolVar(&yes, "yes", false, "apply the zip")
	imp.Flags().BoolVar(&server, "server", false, "also write server.yaml (listen address, sign-in, public_url)")
	imp.Flags().BoolVar(&importDB, "with-db", false, "also replace the database (all tickets) at the next start")
	config.AddCommand(export, imp)
}

func printPlan(out io.Writer, p api.ImportPlan, server, database bool) {
	fmt.Fprintf(out, "Made by omo %s on %s.\n", aicmd.OneLine(p.Manifest.OmoVersion), p.Manifest.CreatedAt.Local().Format("2006-01-02 15:04"))
	counts := map[string]int{}
	for _, f := range p.Files {
		counts[f.Change]++
		if f.Change != api.ImportSame {
			fmt.Fprintf(out, "  %-8s %s\n", f.Change, aicmd.OneLine(f.Path))
		}
	}
	fmt.Fprintf(out, "%d to add, %d to replace, %d unchanged.\n", counts[api.ImportAdd], counts[api.ImportReplace], counts[api.ImportSame])
	switch {
	case p.Server == "":
	case server:
		fmt.Fprintf(out, "server.yaml: %s (applies at the next start).\n", p.Server)
	default:
		fmt.Fprintln(out, "server.yaml: left as it is (--server writes it).")
	}
	switch {
	case !p.Database:
	case database:
		fmt.Fprintln(out, "The database replaces all tickets, comments and history at the next start.")
		if p.AgentsRunning > 0 {
			fmt.Fprintf(out, "%d agents run now: stop them first.\n", p.AgentsRunning)
		}
	default:
		fmt.Fprintln(out, "The zip holds a database; it is left out (--with-db imports it).")
	}
}

// writeNewFile writes r to path through a temporary file next to it, so a
// failed download leaves no partial file.
func writeNewFile(path string, r io.Reader) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".omo-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

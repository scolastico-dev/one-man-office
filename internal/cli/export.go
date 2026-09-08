package cli

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/exporter"
)

func addExportCommands(root *cobra.Command) {
	export := &cobra.Command{Use: "export", Short: "Export office data without exposing runtime state"}
	var output string
	write := func(cmd *cobra.Command, data []byte) error {
		if output == "" {
			_, err := cmd.OutOrStdout().Write(append(data, '\n'))
			return err
		}
		return os.WriteFile(output, append(data, '\n'), 0o644)
	}
	open := func() (*sql.DB, string, error) {
		dir, err := os.Getwd()
		if err != nil {
			return nil, "", err
		}
		path := filepath.Join(dir, ".omo", "omo.db")
		database, err := db.OpenReadOnly(path)
		if err != nil {
			return nil, "", fmt.Errorf("open office database: %w", err)
		}
		return database, dir, nil
	}
	statistics := &cobra.Command{Use: "statistics", Short: "Export aggregate statistics without project or job details", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		database, _, err := open()
		if err != nil {
			return err
		}
		defer database.Close()
		data, err := exporter.Statistics(database)
		if err != nil {
			return err
		}
		return write(cmd, data)
	}}
	dbCmd := &cobra.Command{Use: "db <table|all>", Short: "Export one database table or every table", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := open()
		if err != nil {
			return err
		}
		defer database.Close()
		if args[0] == "all" {
			data, err := exporter.All(database)
			if err != nil {
				return err
			}
			return write(cmd, data)
		}
		data, err := exporter.Table(database, args[0])
		if err != nil {
			return err
		}
		return write(cmd, data)
	}}
	gitCmd := &cobra.Command{Use: "git", Short: "Export durable specs and jobs into file-based YAML", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		database, dir, err := open()
		if err != nil {
			return err
		}
		defer database.Close()
		count, err := exporter.Git(dir, database)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "exported %d job files under .omo/jobs\n", count)
		return nil
	}}
	importCmd := &cobra.Command{Use: "import <job.yaml>", Short: "Import and queue one external Git job handoff", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		database, err := db.Open(filepath.Join(dir, ".omo", "omo.db"))
		if err != nil {
			return fmt.Errorf("open office database: %w", err)
		}
		defer database.Close()
		external, err := exporter.ReadFile(args[0])
		if err != nil {
			return err
		}
		job, err := exporter.Import(database, *external)
		if err != nil {
			return err
		}
		mail := &bus.Store{DB: database, Dir: bus.DBDirectory{DB: database}}
		_, _ = mail.Send(bus.SystemSender, "ceo", "external job imported", fmt.Sprintf("Imported %s as local job #%d: %s", external.Source, job.ID, job.Title), bus.PrioHigh)
		fmt.Fprintf(cmd.OutOrStdout(), "imported external job as #%d (queued)\n", job.ID)
		return nil
	}}
	export.PersistentFlags().StringVarP(&output, "output", "o", "", "write the JSON export to a file instead of stdout")
	export.AddCommand(statistics, dbCmd, gitCmd, importCmd)
	root.AddCommand(export)
}

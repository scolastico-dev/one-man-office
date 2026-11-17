package api

import "time"

// BackupManifest is manifest.json of a settings zip: which omo wrote it, for
// which database schema, when, and whether the database is inside.
type BackupManifest struct {
	// Format is the version of the zip's layout; an omo refuses a zip of a
	// format it does not know.
	Format        int       `json:"format"`
	OmoVersion    string    `json:"omo_version"`
	SchemaVersion int       `json:"schema_version"`
	CreatedAt     time.Time `json:"created_at"`
	Database      bool      `json:"database"`
}

// What an import does to one file.
const (
	ImportAdd     = "add"     // the home has no such file yet
	ImportReplace = "replace" // the zip's file replaces a different one
	ImportSame    = "same"    // the home's file has the same content
)

// ImportFile is one file of an import plan: its path below the omo home and
// what applying the zip does to it (ImportAdd, ImportReplace, ImportSame).
type ImportFile struct {
	Path   string `json:"path"`
	Change string `json:"change"`
}

// ImportPlan is the answer of POST /api/settings/import: the uploaded zip
// passed every check and waits under ID; nothing is changed yet.
type ImportPlan struct {
	ID       string         `json:"id"`
	Manifest BackupManifest `json:"manifest"`
	// Files are the settings files of the zip, server.yaml left out.
	Files []ImportFile `json:"files"`
	// Server is what applying server.yaml would do; "" when the zip has
	// none.
	Server string `json:"server"`
	// Database is set when the zip holds a database.
	Database bool `json:"database"`
	// AgentsRunning counts the agents that run now; the database cannot be
	// imported while any does.
	AgentsRunning int `json:"agents_running"`
}

// ImportApply is the body of POST /api/settings/import/apply.
type ImportApply struct {
	ID string `json:"id"`
	// Server also applies server.yaml (start options, used at the next
	// start).
	Server bool `json:"server"`
	// Database replaces every ticket with the zip's database at the next
	// start.
	Database bool `json:"database"`
}

// ImportResult is the answer of POST /api/settings/import/apply.
type ImportResult struct {
	// Written are the files the import wrote, below the omo home.
	Written []string `json:"written"`
	// Backup is the folder that keeps the files the import replaced
	// (absolute); "" when it replaced none.
	Backup string `json:"backup"`
	// DatabasePending is set when the zip's database replaces the current
	// one at the next start of omo; omo is paused until then.
	DatabasePending bool `json:"database_pending"`
	// RestartNeeded is set when something applies only at the next start
	// (server.yaml, the database).
	RestartNeeded bool `json:"restart_needed"`
}

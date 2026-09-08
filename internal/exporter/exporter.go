// Package exporter produces portable, non-runtime views of an omo database.
package exporter

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/queue"
	"gopkg.in/yaml.v3"
)

var allowedTables = []string{"agents", "events", "incidents", "jobs", "messages", "model_usage_snapshots", "overall_statistics", "plugin_logs", "plugin_runtime", "plugin_storage", "shutdown_contexts"}

// Table returns a JSON array for one safe-listed SQLite table.
func Table(database *sql.DB, name string) ([]byte, error) {
	if !contains(allowedTables, name) {
		return nil, fmt.Errorf("unknown export table %q (choose %s)", name, strings.Join(allowedTables, ", "))
	}
	rows, err := database.Query("SELECT * FROM " + name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		item := make(map[string]any, len(columns))
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				item[columns[i]] = string(bytes)
			} else {
				item[columns[i]] = value
			}
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(result, "", "  ")
}

// All returns every safe-listed table keyed by table name.
func All(database *sql.DB) ([]byte, error) {
	result := make(map[string]json.RawMessage, len(allowedTables))
	for _, name := range allowedTables {
		data, err := Table(database, name)
		if err != nil {
			return nil, err
		}
		result[name] = data
	}
	return json.MarshalIndent(result, "", "  ")
}

// Statistics returns aggregate counts and states, intentionally excluding
// project paths, job titles, goals, mail bodies, and other identifying detail.
func Statistics(database *sql.DB) ([]byte, error) {
	counts := make(map[string]int64, len(allowedTables))
	for _, table := range allowedTables {
		var count int64
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			return nil, err
		}
		counts[table] = count
	}
	states, err := groupedCounts(database, "SELECT state, COUNT(*) FROM jobs GROUP BY state")
	if err != nil {
		return nil, err
	}
	roles, err := groupedCounts(database, "SELECT role, COUNT(*) FROM agents GROUP BY role")
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(map[string]any{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"row_counts":   counts,
		"job_states":   states,
		"agent_roles":  roles,
	}, "", "  ")
}

func groupedCounts(database *sql.DB, query string) (map[string]int64, error) {
	rows, err := database.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var key string
		var count int64
		if err := rows.Scan(&key, &count); err != nil {
			return nil, err
		}
		result[key] = count
	}
	return result, rows.Err()
}

type fileJob struct {
	ID                  int64    `yaml:"id"`
	Title               string   `yaml:"title"`
	Goal                string   `yaml:"goal"`
	Role                string   `yaml:"role"`
	Model               string   `yaml:"model,omitempty"`
	Repo                string   `yaml:"repo,omitempty"`
	State               string   `yaml:"state"`
	Checkpoint          string   `yaml:"checkpoint,omitempty"`
	Assignment          string   `yaml:"assignment,omitempty"`
	ParentJob           int64    `yaml:"parent_job,omitempty"`
	DeveloperModels     []string `yaml:"developer_models,omitempty"`
	ForceDeveloperModel string   `yaml:"force_developer_model,omitempty"`
}

// Git writes durable job/spec descriptions without copying the SQLite
// database. It is safe to rerun: each generated file has a stable hash suffix.
func Git(office string, database *sql.DB) (int, error) {
	jobs, err := (&queue.Store{DB: database}).List()
	if err != nil {
		return 0, err
	}
	written := 0
	for _, job := range jobs {
		path, err := gitJobPath(office, job)
		if err != nil {
			return written, err
		}
		data, err := yaml.Marshal(fileJob{ID: job.ID, Title: job.Title, Goal: job.Goal, Role: job.Role, Model: job.Model, Repo: job.Repo, State: string(job.State), Checkpoint: job.Note, Assignment: job.Assignee, ParentJob: job.ParentJob, DeveloperModels: job.DeveloperModels, ForceDeveloperModel: job.ForceDeveloperModel})
		if err != nil {
			return written, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return written, err
		}
		written++
		if job.Role == "product_manager" {
			specPath := filepath.Join(office, ".omo", "specs", filepath.Base(filepath.Dir(filepath.Dir(path))), filepath.Base(filepath.Dir(path)), filepath.Base(path))
			if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
				return written, err
			}
			if err := os.WriteFile(specPath, data, 0o644); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func gitJobPath(office string, job *queue.Job) (string, error) {
	stamp := time.Now().UTC()
	year, month := stamp.Year(), int(stamp.Month())
	clean := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(strings.TrimSpace(job.Title), "-")
	if clean == "" {
		clean = "job"
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", job.ID, job.Title, job.Goal)))
	dir := "active"
	if job.State == queue.StateDone || job.State == queue.StateFailed || job.State == queue.StateCancelled {
		dir = "completed"
	}
	return filepath.Join(office, ".omo", "jobs", dir, fmt.Sprintf("%04d", year), fmt.Sprintf("%02d", month), fmt.Sprintf("%d-%s-%s.yaml", job.ID, clean, hex.EncodeToString(hash[:])[:12])), nil
}

func contains(items []string, value string) bool {
	return sort.SearchStrings(items, value) < len(items) && items[sort.SearchStrings(items, value)] == value
}

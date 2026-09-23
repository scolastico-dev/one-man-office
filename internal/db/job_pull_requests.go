package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type JobPullRequest struct {
	JobID      int64
	Repo       string
	URL        string
	State      string
	Plugin     string
	Action     string
	RecordedAt string
}

type jobPullRequestEvent struct {
	JobID int64  `json:"job_id"`
	Repo  string `json:"repo"`
	State string `json:"state"`
	URL   string `json:"url"`
}

func UpsertJobPullRequest(d *sql.DB, record JobPullRequest) error {
	if record.JobID == 0 {
		return fmt.Errorf("job pull request: job ID must be nonzero")
	}
	if strings.TrimSpace(record.Repo) == "" {
		return fmt.Errorf("job pull request: repository must not be blank")
	}
	if !strings.HasPrefix(record.URL, "http://") && !strings.HasPrefix(record.URL, "https://") {
		return fmt.Errorf("job pull request: URL must begin with http:// or https://")
	}

	recordedAt := time.Now().UTC().Format(time.RFC3339Nano)
	detail, err := json.Marshal(jobPullRequestEvent{
		JobID: record.JobID,
		Repo:  record.Repo,
		State: record.State,
		URL:   record.URL,
	})
	if err != nil {
		return fmt.Errorf("marshal job pull request event: %w", err)
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO job_pull_requests (job_id, repo, url, state, plugin, action, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id, repo) DO UPDATE SET
			url = excluded.url,
			state = excluded.state,
			plugin = excluded.plugin,
			action = excluded.action,
			recorded_at = excluded.recorded_at`,
		record.JobID, record.Repo, record.URL, record.State, record.Plugin, record.Action, recordedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO events (kind, job_id, detail) VALUES (?, ?, ?)`,
		"job_pull_request", record.JobID, string(detail)); err != nil {
		return err
	}
	return tx.Commit()
}

func JobPullRequestForRepo(q Queryer, jobID int64, repo string) (JobPullRequest, bool, error) {
	var record JobPullRequest
	err := q.QueryRow(`
		SELECT job_id, repo, url, state, plugin, action, recorded_at
		FROM job_pull_requests
		WHERE job_id = ? AND repo = ?`, jobID, repo).
		Scan(&record.JobID, &record.Repo, &record.URL, &record.State, &record.Plugin, &record.Action, &record.RecordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return JobPullRequest{}, false, nil
	}
	if err != nil {
		return JobPullRequest{}, false, err
	}
	return record, true, nil
}

func JobPullRequests(q Queryer, jobID int64) ([]JobPullRequest, error) {
	rows, err := q.Query(`
		SELECT job_id, repo, url, state, plugin, action, recorded_at
		FROM job_pull_requests
		WHERE job_id = ?
		ORDER BY repo`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []JobPullRequest
	for rows.Next() {
		var record JobPullRequest
		if err := rows.Scan(&record.JobID, &record.Repo, &record.URL, &record.State, &record.Plugin, &record.Action, &record.RecordedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

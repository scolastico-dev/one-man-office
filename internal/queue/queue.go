// Package queue owns the persistent job queue and its state machine.
package queue

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

type State string

const (
	StateQueued    State = "queued"
	StateAssigned  State = "assigned"
	StateWorking   State = "working"
	StateReview    State = "review"
	StateMerging   State = "merging"
	StateDone      State = "done"
	StateRework    State = "rework"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

const RestartNote = "this is a restart — inspect the worktree and message history and determine what remains"

// transitions: queued→assigned→working→review→merging→done, plus rework,
// failure, cancellation and requeue-on-death/restart edges.
var transitions = map[State][]State{
	StateQueued:   {StateAssigned, StateFailed, StateCancelled},
	StateAssigned: {StateWorking, StateQueued, StateFailed, StateCancelled},
	// working→merging: PM/freelancer jobs have no review stage and walk
	// working→merging→done (see supervisor.done).
	StateWorking:   {StateReview, StateMerging, StateQueued, StateFailed, StateCancelled},
	StateReview:    {StateMerging, StateRework, StateQueued, StateFailed, StateCancelled},
	StateMerging:   {StateDone, StateReview, StateRework, StateQueued, StateCancelled},
	StateRework:    {StateReview, StateWorking, StateQueued, StateFailed, StateCancelled},
	StateFailed:    {StateQueued},
	StateCancelled: {StateQueued},
}

func ValidTransition(from, to State) bool {
	for _, t := range transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

type Job struct {
	ID                  int64
	Title               string
	Goal                string
	Role                string
	Model               string
	Repo                string
	Worktree            string
	Branch              string
	ParentJob           int64
	State               State
	Assignee            string
	Result              string
	Note                string
	Retries             int
	ReviewRejections    int
	ReviewOverride      bool
	DeveloperModels     []string
	ForceDeveloperModel string
}

type Store struct {
	DB *sql.DB
}

const jobCols = `id, title, goal, role, model, repo, worktree, branch, parent_job, state, assignee, result, note, retries, review_rejections, review_override, developer_models, force_developer_model`

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var developerModels string
	err := row.Scan(&j.ID, &j.Title, &j.Goal, &j.Role, &j.Model, &j.Repo, &j.Worktree,
		&j.Branch, &j.ParentJob, &j.State, &j.Assignee, &j.Result, &j.Note, &j.Retries, &j.ReviewRejections, &j.ReviewOverride,
		&developerModels, &j.ForceDeveloperModel)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(developerModels), &j.DeveloperModels); err != nil {
		return nil, fmt.Errorf("job %d: invalid developer model policy: %w", j.ID, err)
	}
	return &j, nil
}

func (s *Store) Create(j *Job) error {
	developerModels, err := json.Marshal(j.DeveloperModels)
	if err != nil {
		return err
	}
	res, err := s.DB.Exec(
		`INSERT INTO jobs (title, goal, role, model, repo, parent_job, developer_models, force_developer_model) VALUES (?,?,?,?,?,?,?,?)`,
		j.Title, j.Goal, j.Role, j.Model, j.Repo, j.ParentJob, string(developerModels), j.ForceDeveloperModel)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	j.ID = id
	j.State = StateQueued
	return db.AppendEvent(s.DB, "job_created", "", id, j.Role+": "+j.Title)
}

func (s *Store) Get(id int64) (*Job, error) {
	return scanJob(s.DB.QueryRow(`SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
}

func (s *Store) List(states ...State) ([]*Job, error) {
	q := `SELECT ` + jobCols + ` FROM jobs`
	var args []any
	if len(states) > 0 {
		q += ` WHERE state IN (?` + strings.Repeat(",?", len(states)-1) + `)`
		for _, st := range states {
			args = append(args, string(st))
		}
	}
	q += ` ORDER BY id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Transition validates the edge, updates the row and appends a job_state
// event, all in one transaction (state is durable before any verb ack).
func (s *Store) Transition(id int64, to State) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var from State
	if err := tx.QueryRow(`SELECT state FROM jobs WHERE id = ?`, id).Scan(&from); err != nil {
		return err
	}
	if !ValidTransition(from, to) {
		return fmt.Errorf("job %d: invalid transition %s→%s", id, from, to)
	}
	if _, err := tx.Exec(
		`UPDATE jobs SET state = ?, updated_at = datetime('now') WHERE id = ?`, string(to), id); err != nil {
		return err
	}
	if err := db.AppendEvent(tx, "job_state", "", id, string(from)+"→"+string(to)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) set(id int64, col string, val any) error {
	_, err := s.DB.Exec(`UPDATE jobs SET `+col+` = ?, updated_at = datetime('now') WHERE id = ?`, val, id)
	return err
}

func (s *Store) SetAssignee(id int64, name string) error { return s.set(id, "assignee", name) }
func (s *Store) SetResult(id int64, result string) error { return s.set(id, "result", result) }
func (s *Store) SetNote(id int64, note string) error     { return s.set(id, "note", note) }

func (s *Store) SetWorktree(id int64, worktree, branch string) error {
	_, err := s.DB.Exec(
		`UPDATE jobs SET worktree = ?, branch = ?, updated_at = datetime('now') WHERE id = ?`,
		worktree, branch, id)
	return err
}

func (s *Store) IncrementRetries(id int64) (int, error) {
	if _, err := s.DB.Exec(`UPDATE jobs SET retries = retries + 1 WHERE id = ?`, id); err != nil {
		return 0, err
	}
	var n int
	err := s.DB.QueryRow(`SELECT retries FROM jobs WHERE id = ?`, id).Scan(&n)
	return n, err
}

func (s *Store) IncrementReviewRejections(id int64) (int, error) {
	if _, err := s.DB.Exec(`UPDATE jobs SET review_rejections = review_rejections + 1 WHERE id = ?`, id); err != nil {
		return 0, err
	}
	var n int
	err := s.DB.QueryRow(`SELECT review_rejections FROM jobs WHERE id = ?`, id).Scan(&n)
	return n, err
}

func (s *Store) SetReviewOverride(id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	return s.set(id, "review_override", v)
}

func (s *Store) ResetReviewState(id int64) error {
	_, err := s.DB.Exec(`UPDATE jobs SET review_rejections = 0, review_override = 0 WHERE id = ?`, id)
	return err
}

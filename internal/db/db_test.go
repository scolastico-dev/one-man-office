package db

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestModelUsageSnapshotsKeepLatestCheckPerProvider(t *testing.T) {
	d := open(t)
	first := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	if err := UpsertModelUsageSnapshot(d, ModelUsageSnapshot{Provider: "codex", UsedPercent: 42, FetchedAt: first}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertModelUsageSnapshot(d, ModelUsageSnapshot{Provider: "codex", UsedPercent: 63.5, FetchedAt: second}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertModelUsageSnapshot(d, ModelUsageSnapshot{Provider: "claude", UsedPercent: 51, HasSession: true, SessionUsedPercent: 73, FetchedAt: first}); err != nil {
		t.Fatal(err)
	}

	rows, err := ModelUsageSnapshots(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Provider != "claude" || rows[1].Provider != "codex" {
		t.Fatalf("usage rows = %+v", rows)
	}
	if rows[1].UsedPercent != 63.5 || !rows[1].FetchedAt.Equal(second) {
		t.Fatalf("latest codex snapshot = %+v", rows[1])
	}
	if !rows[0].HasSession || rows[0].SessionUsedPercent != 73 {
		t.Fatalf("Claude session snapshot = %+v", rows[0])
	}
	var individualRows int
	if err := d.QueryRow(`SELECT COUNT(*) FROM model_usage_snapshots WHERE profile <> provider`).Scan(&individualRows); err != nil {
		t.Fatal(err)
	}
	if individualRows != 0 {
		t.Fatalf("individual usage rows = %d, want 0", individualRows)
	}
}

func TestModelUsageSnapshotsKeepSeparateCredentialScopes(t *testing.T) {
	d := open(t)
	checked := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	for _, snapshot := range []ModelUsageSnapshot{
		{Provider: "claude", Scope: "claude:/accounts/a/.credentials.json", UsedPercent: 20, FetchedAt: checked},
		{Provider: "claude", Scope: "claude:/accounts/b/.credentials.json", UsedPercent: 70, FetchedAt: checked},
	} {
		if err := UpsertModelUsageSnapshot(d, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ModelUsageSnapshots(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Scope == rows[1].Scope || rows[0].UsedPercent != 20 || rows[1].UsedPercent != 70 {
		t.Fatalf("scoped usage rows = %+v", rows)
	}
}

func TestPruneModelUsageSnapshotsRemovesInactiveCredentialScopes(t *testing.T) {
	d := open(t)
	checked := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	for _, snapshot := range []ModelUsageSnapshot{
		{Provider: "claude", Scope: "claude:/accounts/active/.credentials.json", UsedPercent: 20, FetchedAt: checked},
		{Provider: "claude", Scope: "claude:/accounts/removed/.credentials.json", UsedPercent: 70, FetchedAt: checked},
	} {
		if err := UpsertModelUsageSnapshot(d, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if err := PruneModelUsageSnapshots(d, []string{"claude:/accounts/active/.credentials.json"}); err != nil {
		t.Fatal(err)
	}
	rows, err := ModelUsageSnapshots(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Scope != "claude:/accounts/active/.credentials.json" {
		t.Fatalf("usage rows after prune = %+v", rows)
	}
}

func TestOpenRemovesLegacyIndividualUsageSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO model_usage_snapshots (profile, provider, used_percent, fetched_at) VALUES ('claude-opus', 'claude', 51, '2026-08-23T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO model_usage_snapshots (profile, provider, used_percent, fetched_at) VALUES ('claude:/accounts/work/.credentials.json', 'claude', 42, '2026-08-23T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var rows int
	if err := d.QueryRow(`SELECT COUNT(*) FROM model_usage_snapshots`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("usage rows after migration = %d, want one credential scope", rows)
	}
}

func open(t *testing.T) *sql.DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestOpenIsWAL(t *testing.T) {
	d := open(t)
	var mode string
	if err := d.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestOpenReadOnlyAllowsQueriesAndRejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	writable, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writable.Close()
	if err := AppendEvent(writable, "existing", "", 0, "visible"); err != nil {
		t.Fatal(err)
	}
	if err := UpsertModelUsageSnapshot(writable, ModelUsageSnapshot{Provider: "codex", UsedPercent: 60}); err != nil {
		t.Fatal(err)
	}
	observer, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	if events, err := AllEvents(observer); err != nil || len(events) != 1 {
		t.Fatalf("read events = %+v, %v", events, err)
	}
	if usage, err := ModelUsageSnapshots(observer); err != nil || len(usage) != 1 || usage[0].Provider != "codex" || usage[0].UsedPercent != 60 {
		t.Fatalf("read usage = %+v, %v", usage, err)
	}
	if err := AppendEvent(observer, "forbidden", "", 0, "write"); err == nil {
		t.Fatal("read-only database accepted a write")
	}
}

func TestOpenMigratesLegacyAgentsWithReadyPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE agents (name TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`UPDATE agents SET ready_prompt = ''`); err != nil {
		t.Fatalf("ready_prompt column was not migrated: %v", err)
	}
}

func TestOpenMigratesLegacyAgentsWithIncidentID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE agents (
		name TEXT PRIMARY KEY,
		role TEXT NOT NULL,
		profile TEXT NOT NULL,
		job_id INTEGER NOT NULL DEFAULT 0,
		goal TEXT NOT NULL DEFAULT '',
		workdir TEXT NOT NULL DEFAULT '',
		ready_prompt TEXT NOT NULL DEFAULT '',
		current_step TEXT NOT NULL DEFAULT '',
		step_updated_at TEXT,
		state TEXT NOT NULL DEFAULT 'spawning',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		ended_at TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO agents (name, role, profile, state) VALUES ('old-firefighter', 'firefighter', 'firefighter', 'working')`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var incidentID int64
	if err := d.QueryRow(`SELECT incident_id FROM agents WHERE name = 'old-firefighter'`).Scan(&incidentID); err != nil {
		t.Fatalf("incident_id column was not migrated: %v", err)
	}
	if incidentID != 0 {
		t.Fatalf("legacy incident_id = %d, want 0", incidentID)
	}
	living, err := LivingAgents(d)
	if err != nil {
		t.Fatalf("living agents after migration: %v", err)
	}
	if len(living) != 1 || living[0].IncidentID != 0 {
		t.Fatalf("living agents after migration = %+v, want one zero-owned agent", living)
	}
}

func TestOpenMigratesLegacyJobPullRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		goal TEXT NOT NULL,
		role TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	result, err := d.Exec(`INSERT INTO jobs (title, goal, role) VALUES ('open PR', 'ship it', 'developer')`)
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	record := JobPullRequest{
		JobID:  jobID,
		Repo:   "api",
		URL:    "https://example.test/pull/1",
		State:  "open",
		Plugin: "pullrequest",
		Action: "open",
	}
	if err := UpsertJobPullRequest(d, record); err != nil {
		t.Fatal(err)
	}
	got, ok, err := JobPullRequestForRepo(d, jobID, record.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.JobID != record.JobID || got.Repo != record.Repo || got.URL != record.URL || got.State != record.State || got.Plugin != record.Plugin || got.Action != record.Action || got.RecordedAt == "" {
		t.Fatalf("pull request = %+v, found = %v", got, ok)
	}
}

func TestUpsertJobPullRequestIsIdempotentAndAudited(t *testing.T) {
	d := open(t)
	jobID := insertTestJob(t, d)
	first := JobPullRequest{
		JobID:  jobID,
		Repo:   "api",
		URL:    "https://example.test/pull/1",
		State:  "open",
		Plugin: "pullrequest",
		Action: "open",
	}
	second := first
	second.URL = "https://example.test/pull/2"
	second.State = "merged"
	if err := UpsertJobPullRequest(d, first); err != nil {
		t.Fatal(err)
	}
	if err := UpsertJobPullRequest(d, second); err != nil {
		t.Fatal(err)
	}

	rows, err := JobPullRequests(d, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].URL != second.URL || rows[0].State != second.State {
		t.Fatalf("pull requests = %+v", rows)
	}
	events, err := AllEvents(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v, want 2", events)
	}
	for _, event := range events {
		if event.Kind != "job_pull_request" || event.JobID != jobID {
			t.Fatalf("event = %+v", event)
		}
		var detail map[string]json.RawMessage
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			t.Fatalf("event detail %q: %v", event.Detail, err)
		}
		if len(detail) != 4 {
			t.Fatalf("event detail keys = %v, want exactly job_id, repo, state, url", detail)
		}
		for _, key := range []string{"job_id", "repo", "state", "url"} {
			if _, ok := detail[key]; !ok {
				t.Fatalf("event detail keys = %v, missing %q", detail, key)
			}
		}
		var detailJobID int64
		var detailRepo, detailState, detailURL string
		if err := json.Unmarshal(detail["job_id"], &detailJobID); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(detail["repo"], &detailRepo); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(detail["state"], &detailState); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(detail["url"], &detailURL); err != nil {
			t.Fatal(err)
		}
		firstDetail := detailState == first.State && detailURL == first.URL
		secondDetail := detailState == second.State && detailURL == second.URL
		if detailJobID != jobID || detailRepo != first.Repo || (!firstDetail && !secondDetail) {
			t.Fatalf("event detail values = %s, want one of the recorded pull requests", event.Detail)
		}
	}
}

func TestJobPullRequestsSortByRepoAndDistinguishMissing(t *testing.T) {
	d := open(t)
	jobID := insertTestJob(t, d)
	for _, repo := range []string{"zeta", "alpha"} {
		if err := UpsertJobPullRequest(d, JobPullRequest{
			JobID: jobID,
			Repo:  repo,
			URL:   "https://example.test/" + repo,
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := JobPullRequests(d, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Repo != "alpha" || rows[1].Repo != "zeta" {
		t.Fatalf("pull requests = %+v, want alpha then zeta", rows)
	}
	if _, found, err := JobPullRequestForRepo(d, jobID, "missing"); err != nil || found {
		t.Fatalf("missing pull request = found %v, err %v; want false, nil", found, err)
	}
}

func TestUpsertJobPullRequestRollsBackWhenEventInsertFails(t *testing.T) {
	d := open(t)
	jobID := insertTestJob(t, d)
	if _, err := d.Exec(`
		CREATE TRIGGER fail_job_pull_request_event
		BEFORE INSERT ON events
		WHEN NEW.kind = 'job_pull_request'
		BEGIN
			SELECT RAISE(ABORT, 'event rejected');
		END`); err != nil {
		t.Fatal(err)
	}
	if err := UpsertJobPullRequest(d, JobPullRequest{
		JobID: jobID,
		Repo:  "api",
		URL:   "https://example.test/pull/1",
	}); err == nil {
		t.Fatal("UpsertJobPullRequest succeeded despite rejected event")
	}
	var pullRequests int
	if err := d.QueryRow(`SELECT COUNT(*) FROM job_pull_requests`).Scan(&pullRequests); err != nil {
		t.Fatal(err)
	}
	if pullRequests != 0 {
		t.Fatalf("pull requests after event failure = %d, want 0", pullRequests)
	}
}

func TestUpsertJobPullRequestRejectsInvalidInputWithoutPartialState(t *testing.T) {
	d := open(t)
	jobID := insertTestJob(t, d)
	tests := []JobPullRequest{
		{JobID: 0, Repo: "api", URL: "https://example.test/pull/1"},
		{JobID: jobID, Repo: "   ", URL: "https://example.test/pull/1"},
		{JobID: jobID, Repo: "api", URL: "ftp://example.test/pull/1"},
		{JobID: 999999, Repo: "missing", URL: "https://example.test/pull/1"},
	}
	for _, record := range tests {
		if err := UpsertJobPullRequest(d, record); err == nil {
			t.Fatalf("UpsertJobPullRequest(%+v) succeeded", record)
		}
	}
	var pullRequests, events int
	if err := d.QueryRow(`SELECT COUNT(*) FROM job_pull_requests`).Scan(&pullRequests); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'job_pull_request'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if pullRequests != 0 || events != 0 {
		t.Fatalf("partial state: pull requests = %d, events = %d", pullRequests, events)
	}
}

func insertTestJob(t *testing.T, d *sql.DB) int64 {
	t.Helper()
	result, err := d.Exec(`INSERT INTO jobs (title, goal, role) VALUES ('test job', 'test goal', 'developer')`)
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return jobID
}

func TestAgentLifecycle(t *testing.T) {
	d := open(t)
	a := Agent{Name: "developer-jason", Role: "developer", Profile: "sonnet", JobID: 7, Goal: "build it", WorkDir: "/worktrees/job-7", IncidentID: 41}
	if err := InsertAgent(d, a); err != nil {
		t.Fatal(err)
	}
	got, err := GetAgent(d, "developer-jason")
	if err != nil || got.State != "spawning" || got.JobID != 7 || got.WorkDir != "/worktrees/job-7" || got.IncidentID != 41 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if err := SetAgentState(d, "developer-jason", "working"); err != nil {
		t.Fatal(err)
	}
	if err := SetAgentStep(d, "developer-jason", "running integration tests"); err != nil {
		t.Fatal(err)
	}
	if err := SetAgentReadyPrompt(d, "developer-jason", "the exact ready prompt"); err != nil {
		t.Fatal(err)
	}
	got, _ = GetAgent(d, "developer-jason")
	if got.Step != "running integration tests" || !got.StepUpdatedAt.Valid || got.ReadyPrompt != "the exact ready prompt" {
		t.Fatalf("step not persisted: %+v", got)
	}
	if n, _ := CountLivingByRole(d, "developer"); n != 1 {
		t.Fatalf("living developers = %d, want 1", n)
	}
	if err := MarkAllAgentsDead(d); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountLivingByRole(d, "developer"); n != 0 {
		t.Fatalf("living after MarkAllAgentsDead = %d, want 0", n)
	}
}

func TestEvents(t *testing.T) {
	d := open(t)
	if err := AppendEvent(d, "agent_spawned", "pm-alex", 0, "role=product_manager"); err != nil {
		t.Fatal(err)
	}
	if err := AppendEvent(d, "job_state", "", 3, "queued→assigned"); err != nil {
		t.Fatal(err)
	}
	evs, err := EventsSince(d, 0)
	if err != nil || len(evs) != 2 {
		t.Fatalf("events = %v err %v", evs, err)
	}
	last, _ := LastEventID(d)
	evs, _ = EventsSince(d, last)
	if len(evs) != 0 {
		t.Fatalf("expected no events after last id, got %d", len(evs))
	}
	all, err := AllEvents(d)
	if err != nil || len(all) != 2 || all[0].ID < all[1].ID {
		t.Fatalf("AllEvents = %+v err %v", all, err)
	}
	if count, err := CountEvents(d); err != nil || count != 2 {
		t.Fatalf("CountEvents = %d, %v", count, err)
	}
	page, err := EventsPage(d, 1, 1)
	if err != nil || len(page) != 1 || page[0].ID != all[1].ID {
		t.Fatalf("EventsPage = %+v, %v", page, err)
	}
}

func TestAllIncidentsNewestFirst(t *testing.T) {
	d := open(t)
	if _, err := d.Exec(`INSERT INTO incidents (agent, class, detail) VALUES
		('developer-jason', 'stuck', 'no output'),
		('pm-alex', 'loop', 'same command')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE incidents SET state = 'resolved', resolved_at = datetime('now') WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	incidents, err := AllIncidents(d)
	if err != nil || len(incidents) != 2 {
		t.Fatalf("AllIncidents = %+v err %v", incidents, err)
	}
	if incidents[0].Agent != "pm-alex" || incidents[1].State != "resolved" || !incidents[1].ResolvedAt.Valid {
		t.Fatalf("unexpected incident history: %+v", incidents)
	}
}

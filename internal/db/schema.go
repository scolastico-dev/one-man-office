package db

// migrations holds one script per schema version; index i brings the database
// from version i to i+1.
var migrations = []string{
	// Version 1.
	`CREATE TABLE agent_sessions (
  id          TEXT PRIMARY KEY,
  role        TEXT NOT NULL CHECK (role IN ('ceo','pm','developer','reviewer')),
  ticket_id   INTEGER REFERENCES tickets(id),
  project_id  TEXT NOT NULL DEFAULT '',
  name        TEXT NOT NULL,
  model       TEXT NOT NULL,
  state       TEXT NOT NULL CHECK (state IN ('starting','running','waiting','ended')),
  workdir     TEXT NOT NULL DEFAULT '',
  log_path    TEXT NOT NULL DEFAULT '',
  end_reason  TEXT NOT NULL DEFAULT '',
  started_at  TEXT NOT NULL,
  ended_at    TEXT,
  CHECK ((role = 'ceo') = (ticket_id IS NULL)),
  CHECK ((state = 'ended') = (ended_at IS NOT NULL))
);
CREATE INDEX agent_sessions_ticket ON agent_sessions(ticket_id, role, state);

CREATE TABLE tickets (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  type                TEXT NOT NULL CHECK (type IN ('story','task')),
  title               TEXT NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 200),
  col                 TEXT NOT NULL DEFAULT 'planning'
                        CHECK (col IN ('planning','ready','in_progress','review','done')),
  resolution          TEXT NOT NULL DEFAULT ''
                        CHECK (resolution IN ('','completed','cancelled','exported')),
  rank                REAL NOT NULL,
  story_id            INTEGER REFERENCES tickets(id),
  supervisor          TEXT NOT NULL CHECK (supervisor IN ('user','ceo','pm')),
  created_by_role     TEXT NOT NULL CHECK (created_by_role IN ('user','ceo','pm')),
  created_by_session  TEXT REFERENCES agent_sessions(id),
  description_version INTEGER NOT NULL DEFAULT 0,
  model               TEXT NOT NULL DEFAULT '',
  review              TEXT NOT NULL DEFAULT ''
                        CHECK (review IN ('','skipped','agent','human','agent_then_human')),
  when_approved       TEXT NOT NULL DEFAULT '' CHECK (when_approved IN ('','merge','export')),
  task_release        TEXT NOT NULL DEFAULT '' CHECK (task_release IN ('','agents','user')),
  max_parallel        INTEGER CHECK (max_parallel IS NULL OR max_parallel > 0),
  review_stage        TEXT NOT NULL DEFAULT ''
                        CHECK (review_stage IN ('','agent','human','merging','exporting')),
  review_round        INTEGER NOT NULL DEFAULT 0,
  attention           TEXT NOT NULL DEFAULT '',
  export_url          TEXT NOT NULL DEFAULT '',
  export_merged       INTEGER NOT NULL DEFAULT 0 CHECK (export_merged IN (0,1)),
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  closed_at           TEXT,
  CHECK ((col = 'done') = (resolution <> '')),
  CHECK ((col = 'done') = (closed_at IS NOT NULL)),
  CHECK (type = 'task' OR story_id IS NULL),
  CHECK (type = 'story' OR (task_release = '' AND max_parallel IS NULL)),
  CHECK (col = 'review' OR review_stage = '')
);
CREATE INDEX tickets_col_rank ON tickets(col, rank);
CREATE INDEX tickets_story ON tickets(story_id);

CREATE TABLE ticket_projects (
  ticket_id   INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  project_id  TEXT NOT NULL,
  position    INTEGER NOT NULL,
  base_branch TEXT NOT NULL DEFAULT '',
  branch      TEXT NOT NULL DEFAULT '',
  worktree    TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (ticket_id, project_id)
);
CREATE INDEX ticket_projects_project ON ticket_projects(project_id);

CREATE TABLE dependencies (
  ticket_id     INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  depends_on_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  created_at    TEXT NOT NULL,
  PRIMARY KEY (ticket_id, depends_on_id),
  CHECK (ticket_id <> depends_on_id)
);
CREATE INDEX dependencies_reverse ON dependencies(depends_on_id);

CREATE TABLE description_versions (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id      INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  number         INTEGER NOT NULL,
  body           TEXT NOT NULL,
  author_role    TEXT NOT NULL CHECK (author_role IN ('user','ceo','pm','developer','reviewer','system')),
  author_session TEXT REFERENCES agent_sessions(id),
  restored_from  INTEGER,
  created_at     TEXT NOT NULL,
  UNIQUE (ticket_id, number)
);

CREATE TABLE comments (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id      INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  author_role    TEXT NOT NULL CHECK (author_role IN ('user','ceo','pm','developer','reviewer','system')),
  author_session TEXT REFERENCES agent_sessions(id),
  body           TEXT NOT NULL CHECK (length(trim(body)) > 0),
  reply_to       INTEGER REFERENCES comments(id),
  question_to    TEXT NOT NULL DEFAULT '' CHECK (question_to IN ('','user','ceo','pm')),
  answered_by    INTEGER REFERENCES comments(id),
  created_at     TEXT NOT NULL
);
CREATE INDEX comments_ticket ON comments(ticket_id, id);
CREATE INDEX comments_open_questions ON comments(question_to) WHERE question_to <> '' AND answered_by IS NULL;

CREATE TABLE comment_mentions (
  comment_id INTEGER NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
  role       TEXT NOT NULL CHECK (role IN ('user','ceo','pm','developer','reviewer')),
  session_id TEXT REFERENCES agent_sessions(id),
  PRIMARY KEY (comment_id, role)
);

CREATE TABLE review_verdicts (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id   INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  round       INTEGER NOT NULL,
  project_id  TEXT NOT NULL,
  session_id  TEXT REFERENCES agent_sessions(id),
  verdict     TEXT NOT NULL CHECK (verdict IN ('approve','reject')),
  comment_id  INTEGER REFERENCES comments(id),
  created_at  TEXT NOT NULL,
  UNIQUE (ticket_id, round, project_id)
);

CREATE TABLE events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  kind       TEXT NOT NULL,
  ticket_id  INTEGER REFERENCES tickets(id) ON DELETE SET NULL,
  actor      TEXT NOT NULL DEFAULT '',
  detail     TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX events_ticket ON events(ticket_id, id);

CREATE TABLE server_state (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS plugin_storage (
  scope      TEXT NOT NULL CHECK(scope IN ('global', 'local')),
  plugin     TEXT NOT NULL DEFAULT '',
  key        TEXT NOT NULL,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY(scope, plugin, key)
);
CREATE TABLE IF NOT EXISTS plugin_runtime (
  name          TEXT PRIMARY KEY,
  version       TEXT NOT NULL DEFAULT '',
  description   TEXT NOT NULL DEFAULT '',
  state         TEXT NOT NULL,
  hook_count    INTEGER NOT NULL DEFAULT 0,
  last_event    TEXT NOT NULL DEFAULT '',
  last_run_at   TEXT NOT NULL DEFAULT '',
  last_log      TEXT NOT NULL DEFAULT '',
  last_log_at   TEXT NOT NULL DEFAULT '',
  updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS plugin_logs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  plugin      TEXT NOT NULL REFERENCES plugin_runtime(name) ON DELETE CASCADE,
  message     TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_plugin_logs_plugin_id ON plugin_logs(plugin, id);
`, // Version 2: engine claims, failed steps, start blockers, export URLs per
	// project and agent session purpose and review round.
	`ALTER TABLE tickets ADD COLUMN busy INTEGER NOT NULL DEFAULT 0 CHECK (busy IN (0,1));
ALTER TABLE tickets ADD COLUMN failed_step TEXT NOT NULL DEFAULT '' CHECK (failed_step IN ('','start','agent','merge'));
ALTER TABLE ticket_projects ADD COLUMN export_url TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_sessions ADD COLUMN purpose TEXT NOT NULL DEFAULT 'work' CHECK (purpose IN ('work','export'));
ALTER TABLE agent_sessions ADD COLUMN review_round INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tickets ADD COLUMN start_blocked TEXT NOT NULL DEFAULT '' CHECK (start_blocked IN ('','slot','paused','no_model','usage'));
`, // Version 3: what the export of a project left out or failed with.
	`ALTER TABLE ticket_projects ADD COLUMN export_state TEXT NOT NULL DEFAULT '' CHECK (export_state IN ('','failed','left_out'));
ALTER TABLE ticket_projects ADD COLUMN export_error TEXT NOT NULL DEFAULT '';
`,
}

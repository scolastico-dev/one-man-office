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
`, // Version 4: uploaded images, one row per file in the images folder.
	`CREATE TABLE images (
  id             TEXT PRIMARY KEY,
  ticket_id      INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  author_role    TEXT NOT NULL CHECK (author_role IN ('user','ceo','pm','developer','reviewer')),
  author_session TEXT REFERENCES agent_sessions(id),
  name           TEXT NOT NULL,
  content_type   TEXT NOT NULL CHECK (content_type IN ('image/png','image/jpeg','image/gif','image/webp')),
  size           INTEGER NOT NULL CHECK (size > 0),
  created_at     TEXT NOT NULL,
  expired_at     TEXT
);
CREATE INDEX images_ticket ON images(ticket_id);
`, // Version 5: the working folder of a task without a project.
	`ALTER TABLE tickets ADD COLUMN workdir TEXT NOT NULL DEFAULT '';
`, // Version 6: the commits a project's diff runs between, recorded when
	// the ticket is merged or exported, and the review round they belong to.
	`ALTER TABLE ticket_projects ADD COLUMN start_commit TEXT NOT NULL DEFAULT '';
ALTER TABLE ticket_projects ADD COLUMN end_commit TEXT NOT NULL DEFAULT '';
ALTER TABLE ticket_projects ADD COLUMN range_round INTEGER NOT NULL DEFAULT 0;
`, // Version 7: the personal access tokens and OAuth grants of MCP clients
	// (only hashes of their secrets) and omo's own sign-ins at upstream MCP
	// servers (stored as they are).
	`CREATE TABLE mcp_tokens (
  id                 TEXT PRIMARY KEY,
  kind               TEXT NOT NULL CHECK (kind IN ('pat','oauth')),
  name               TEXT NOT NULL,
  client_id          TEXT NOT NULL DEFAULT '',
  redirect_host      TEXT NOT NULL DEFAULT '',
  resource           TEXT NOT NULL DEFAULT '',
  scope              TEXT NOT NULL DEFAULT 'mcp',
  token_hash         TEXT NOT NULL UNIQUE,
  refresh_hash       TEXT UNIQUE,
  prior_refresh_hash TEXT,
  created_at         TEXT NOT NULL,
  expires_at         TEXT,
  refresh_expires_at TEXT,
  last_used_at       TEXT,
  revoked_at         TEXT
);
CREATE TABLE mcp_upstream_auth (
  server        TEXT PRIMARY KEY,
  issuer        TEXT NOT NULL,
  client_id     TEXT NOT NULL,
  client_secret TEXT NOT NULL DEFAULT '',
  registration  TEXT NOT NULL DEFAULT '{}',
  redirect_uri  TEXT NOT NULL,
  access_token  TEXT NOT NULL DEFAULT '',
  refresh_token TEXT NOT NULL DEFAULT '',
  token_type    TEXT NOT NULL DEFAULT 'Bearer',
  expiry        TEXT,
  scopes        TEXT NOT NULL DEFAULT '',
  state         TEXT NOT NULL,
  error         TEXT NOT NULL DEFAULT '',
  updated_at    TEXT NOT NULL
);
`, // Version 8: persistent web UI forwards, global or owned by a ticket.
	`CREATE TABLE proxy_forwards (
  id           TEXT PRIMARY KEY,
  ticket_id    INTEGER REFERENCES tickets(id) ON DELETE CASCADE,
  host         TEXT NOT NULL,
  port         INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
  scheme       TEXT NOT NULL CHECK (scheme IN ('http','https')),
  name         TEXT NOT NULL,
  start_path   TEXT NOT NULL,
  strip_prefix INTEGER NOT NULL DEFAULT 0 CHECK (strip_prefix IN (0,1)),
  verify_tls   INTEGER NOT NULL DEFAULT 0 CHECK (verify_tls IN (0,1))
);
CREATE INDEX proxy_forwards_ticket ON proxy_forwards(ticket_id);
`,
	// Version 9: exports wait in Review until their pushed commits are merged.
	`CREATE TABLE tickets_new (
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
                        CHECK (review_stage IN ('','agent','human','merging','exporting','awaiting_merge')),
  review_round        INTEGER NOT NULL DEFAULT 0,
  attention           TEXT NOT NULL DEFAULT '',
  export_url          TEXT NOT NULL DEFAULT '',
  export_merged       INTEGER NOT NULL DEFAULT 0 CHECK (export_merged IN (0,1)),
  busy INTEGER NOT NULL DEFAULT 0 CHECK (busy IN (0,1)),
  failed_step TEXT NOT NULL DEFAULT '' CHECK (failed_step IN ('','start','agent','merge')),
  start_blocked TEXT NOT NULL DEFAULT '' CHECK (start_blocked IN ('','slot','paused','no_model','usage')),
  workdir TEXT NOT NULL DEFAULT '',
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  closed_at           TEXT,
  CHECK ((col = 'done') = (resolution <> '')),
  CHECK ((col = 'done') = (closed_at IS NOT NULL)),
  CHECK (type = 'task' OR story_id IS NULL),
  CHECK (type = 'story' OR (task_release = '' AND max_parallel IS NULL)),
  CHECK (col = 'review' OR review_stage = '')
);
INSERT INTO tickets_new (id,type,title,col,resolution,rank,story_id,supervisor,created_by_role,created_by_session,description_version,model,review,when_approved,task_release,max_parallel,review_stage,review_round,attention,export_url,export_merged,created_at,updated_at,closed_at,busy,failed_step,start_blocked,workdir) SELECT id,type,title,col,resolution,rank,story_id,supervisor,created_by_role,created_by_session,description_version,model,review,when_approved,task_release,max_parallel,review_stage,review_round,attention,export_url,export_merged,created_at,updated_at,closed_at,busy,failed_step,start_blocked,workdir FROM tickets;
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'tickets'), 0)) WHERE name = 'tickets_new';
DROP TABLE tickets;
ALTER TABLE tickets_new RENAME TO tickets;
CREATE INDEX tickets_col_rank ON tickets(col, rank);
CREATE INDEX tickets_story ON tickets(story_id);
ALTER TABLE ticket_projects ADD COLUMN export_remote TEXT NOT NULL DEFAULT '';
ALTER TABLE ticket_projects ADD COLUMN export_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE ticket_projects ADD COLUMN export_base TEXT NOT NULL DEFAULT '';
ALTER TABLE ticket_projects ADD COLUMN export_commit TEXT NOT NULL DEFAULT '';
ALTER TABLE ticket_projects ADD COLUMN export_target_commit TEXT NOT NULL DEFAULT '';
`,
	// Version 10: plugin ownership is assigned only by server-bound helpers.
	`ALTER TABLE proxy_forwards ADD COLUMN plugin_owner TEXT NOT NULL DEFAULT '';
ALTER TABLE proxy_forwards ADD COLUMN task_id TEXT NOT NULL DEFAULT '';
CREATE INDEX proxy_forwards_plugin ON proxy_forwards(plugin_owner,task_id);
`,
	// Version 11: attention ownership is state, independent of display text.
	`ALTER TABLE agent_sessions ADD COLUMN end_kind TEXT NOT NULL DEFAULT '';
UPDATE agent_sessions SET end_kind = 'user' WHERE end_reason = 'stopped by the user';
ALTER TABLE tickets ADD COLUMN attention_owner TEXT NOT NULL DEFAULT '';
UPDATE tickets SET attention_owner = 'planner'
WHERE failed_step = '' AND (
  (attention = 'no usable model for the pm role; set models in roles/pm.yaml' AND type = 'story' AND col = 'in_progress')
  OR (attention = 'no usable model for the developer role; set models in roles/developer.yaml'
    AND ((type = 'task' AND col = 'in_progress') OR (col = 'review' AND review_stage = 'exporting')))
  OR (attention = 'no usable model for the reviewer role; set models in roles/reviewer.yaml' AND col = 'review' AND review_stage = 'agent')
  OR ((col IN ('ready','in_progress') OR (col = 'review' AND review_stage IN ('agent','exporting'))) AND EXISTS (
    SELECT 1 FROM ticket_projects p WHERE p.ticket_id = tickets.id
      AND tickets.attention = 'project ' || p.project_id || ' is missing or disabled; omo starts no agent on this ticket until it is available again'
  ))
);
UPDATE tickets SET attention_owner = 'export'
WHERE col = 'review' AND review_stage = 'human' AND failed_step = ''
  AND attention <> '' AND attention = (
    SELECT group_concat(message, '; ') FROM (
      SELECT 'Export of ' || project_id || ' failed: ' || export_error AS message
      FROM ticket_projects WHERE ticket_id = tickets.id AND export_state = 'failed' ORDER BY position
    )
  );
UPDATE tickets SET attention_owner = 'merge_check'
WHERE col = 'review' AND review_stage = 'awaiting_merge' AND failed_step = ''
  AND attention LIKE 'Merge check failed: %';
`,
	// Version 12: plugin gates on a ticket's move. One row per ticket that
	// waits for gates (the move, who asked for it, where it goes) and one
	// row per gating plugin with its answer so far.
	`CREATE TABLE IF NOT EXISTS ticket_gate_moves (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id     INTEGER NOT NULL UNIQUE REFERENCES tickets(id),
  move          TEXT NOT NULL CHECK (move IN ('release','start')),
  actor_role    TEXT NOT NULL,
  actor_name    TEXT NOT NULL DEFAULT '',
  before_ticket INTEGER,
  created_at    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ticket_gates (
  ticket_id  INTEGER NOT NULL REFERENCES ticket_gate_moves(ticket_id) ON DELETE CASCADE,
  plugin     TEXT NOT NULL,
  failure_hold INTEGER NOT NULL DEFAULT 0 CHECK (failure_hold IN (0,1)),
  state      TEXT NOT NULL CHECK (state IN ('pending','held','passed')),
  reason     TEXT NOT NULL DEFAULT '',
  choices    TEXT NOT NULL DEFAULT '[]',
  updated_at TEXT NOT NULL,
  PRIMARY KEY (ticket_id, plugin)
);
`,
}

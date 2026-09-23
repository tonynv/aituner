CREATE TABLE tenants (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL
);

CREATE TABLE machines (
  id          TEXT NOT NULL,
  tenant_id   TEXT NOT NULL REFERENCES tenants(id),
  fingerprint TEXT NOT NULL,
  snapshot    TEXT NOT NULL,
  detected_at INTEGER NOT NULL,
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, fingerprint)
);

CREATE TABLE runs (
  id         TEXT NOT NULL,
  tenant_id  TEXT NOT NULL REFERENCES tenants(id),
  machine_id TEXT NOT NULL,
  phase      TEXT NOT NULL,
  note       TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, machine_id) REFERENCES machines(tenant_id, id)
);
CREATE INDEX runs_recent ON runs(tenant_id, created_at DESC);

CREATE TABLE bench_results (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  tenant_id TEXT NOT NULL,
  run_id    TEXT NOT NULL,
  stage     TEXT NOT NULL CHECK (stage IN ('baseline','tuned')),
  suite     TEXT NOT NULL,
  engine    TEXT NOT NULL,
  metric    TEXT NOT NULL,
  value     REAL NOT NULL,
  unit      TEXT NOT NULL,
  trials    TEXT NOT NULL DEFAULT '[]',
  versions  TEXT NOT NULL DEFAULT '{}',
  thermal   TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  FOREIGN KEY (tenant_id, run_id) REFERENCES runs(tenant_id, id)
);
CREATE INDEX bench_by_run ON bench_results(tenant_id, run_id, stage);

CREATE TABLE tune_changes (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  tenant_id   TEXT NOT NULL,
  run_id      TEXT NOT NULL,
  key         TEXT NOT NULL,
  before      TEXT NOT NULL,
  after       TEXT NOT NULL,
  applied_at  INTEGER NOT NULL,
  reverted_at INTEGER,
  FOREIGN KEY (tenant_id, run_id) REFERENCES runs(tenant_id, id)
);
CREATE INDEX tune_by_run ON tune_changes(tenant_id, run_id);

CREATE TABLE reco_cache (
  tenant_id  TEXT NOT NULL REFERENCES tenants(id),
  source     TEXT NOT NULL,
  key        TEXT NOT NULL,
  body       TEXT NOT NULL,
  fetched_at INTEGER NOT NULL,
  PRIMARY KEY (tenant_id, source, key)
);

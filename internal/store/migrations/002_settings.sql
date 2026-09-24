CREATE TABLE settings (
  tenant_id  TEXT NOT NULL REFERENCES tenants(id),
  key        TEXT NOT NULL,
  value      TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (tenant_id, key)
);

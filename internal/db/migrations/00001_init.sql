-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE TABLE employees (
  id BIGSERIAL PRIMARY KEY,
  feishu_open_id TEXT UNIQUE,
  feishu_union_id TEXT NOT NULL DEFAULT '',
  feishu_user_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  email TEXT NOT NULL DEFAULT '',
  avatar_url TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  is_super_admin BOOLEAN NOT NULL DEFAULT FALSE,
  notice_ack_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE departments (
  id BIGSERIAL PRIMARY KEY,
  feishu_dept_id TEXT UNIQUE NOT NULL,
  parent_feishu_dept_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  leader_open_ids TEXT[] NOT NULL DEFAULT '{}',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE employee_departments (
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  department_id BIGINT NOT NULL REFERENCES departments(id) ON DELETE CASCADE,
  PRIMARY KEY (employee_id, department_id)
);

CREATE TABLE ip_groups (
  id BIGSERIAL PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  proxy_url_enc BYTEA NOT NULL,
  capacity INT NOT NULL DEFAULT 10 CHECK (capacity > 0),
  expected_egress_ip TEXT NOT NULL DEFAULT '',
  last_egress_ip TEXT NOT NULL DEFAULT '',
  last_checked_at TIMESTAMPTZ,
  last_check_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE upstream_accounts (
  id BIGSERIAL PRIMARY KEY,
  cpa_auth_name TEXT UNIQUE NOT NULL,
  email TEXT NOT NULL DEFAULT '',
  plan_type TEXT NOT NULL DEFAULT '',
  owner_type TEXT NOT NULL DEFAULT 'company' CHECK (owner_type IN ('company','employee')),
  employee_id BIGINT UNIQUE REFERENCES employees(id) ON DELETE SET NULL,
  ip_group_id BIGINT NOT NULL REFERENCES ip_groups(id),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  needs_login BOOLEAN NOT NULL DEFAULT FALSE,
  rate_limited_until TIMESTAMPTZ,
  max_concurrency INT CHECK (max_concurrency IS NULL OR max_concurrency > 0),
  device_profile JSONB NOT NULL,
  cpa_drift TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account_onboardings (
  state TEXT PRIMARY KEY,
  ip_group_id BIGINT NOT NULL REFERENCES ip_groups(id),
  os_profile JSONB NOT NULL,
  known_auth_names TEXT[] NOT NULL,
  created_by BIGINT REFERENCES employees(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_account_id BIGINT REFERENCES upstream_accounts(id)
);

CREATE TABLE api_keys (
  id BIGSERIAL PRIMARY KEY,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  key_hash BYTEA UNIQUE NOT NULL,
  key_prefix TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX api_keys_one_active ON api_keys(employee_id) WHERE revoked_at IS NULL;

CREATE TABLE sessions (
  id BIGSERIAL PRIMARY KEY,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  account_id BIGINT REFERENCES upstream_accounts(id) ON DELETE SET NULL,
  session_key TEXT NOT NULL,
  client_kind TEXT NOT NULL,
  first_at TIMESTAMPTZ NOT NULL,
  last_at TIMESTAMPTZ NOT NULL,
  request_count INT NOT NULL DEFAULT 0,
  last_request_id UUID,
  last_req_hashes BYTEA,
  last_req_prefix_of UUID,
  last_req_prefix_len INT NOT NULL DEFAULT 0,
  last_req_chain_depth INT NOT NULL DEFAULT 0,
  last_input_count INT NOT NULL DEFAULT 0,
  UNIQUE (employee_id, session_key)
);

CREATE TABLE requests (
  id UUID PRIMARY KEY,
  started_at TIMESTAMPTZ NOT NULL,
  employee_id BIGINT NOT NULL,
  account_id BIGINT,
  api_key_id BIGINT,
  session_id BIGINT,
  client_kind TEXT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  endpoint TEXT NOT NULL,
  stream BOOLEAN NOT NULL DEFAULT FALSE,
  model_requested TEXT NOT NULL DEFAULT '',
  model_responded TEXT NOT NULL DEFAULT '',
  reasoning_effort TEXT NOT NULL DEFAULT '',
  status_code INT NOT NULL,
  error_type TEXT NOT NULL DEFAULT '',
  error_body TEXT NOT NULL DEFAULT '',
  queued_ms INT NOT NULL DEFAULT 0,
  ttft_ms INT,
  duration_ms INT NOT NULL DEFAULT 0,
  tokens_input BIGINT NOT NULL DEFAULT 0,
  tokens_cached BIGINT NOT NULL DEFAULT 0,
  tokens_output BIGINT NOT NULL DEFAULT 0,
  tokens_reasoning BIGINT NOT NULL DEFAULT 0,
  req_prefix_of UUID,
  req_prefix_len INT NOT NULL DEFAULT 0,
  req_chain_depth INT NOT NULL DEFAULT 0,
  req_hashes BYTEA,
  req_input_start INT NOT NULL DEFAULT 0,
  resp_hashes BYTEA,
  bodies_purged BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX requests_emp_time ON requests(employee_id, started_at DESC);
CREATE INDEX requests_acct_time ON requests(account_id, started_at DESC);
CREATE INDEX requests_session_time ON requests(session_id, started_at DESC);
CREATE INDEX requests_time ON requests(started_at DESC);
CREATE INDEX requests_prefix_of ON requests(req_prefix_of) WHERE req_prefix_of IS NOT NULL;

CREATE TABLE blobs (
  hash BYTEA PRIMARY KEY,
  kind TEXT NOT NULL,
  data_zstd BYTEA NOT NULL,
  raw_size INT NOT NULL,
  last_referenced_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX blobs_last_ref ON blobs(last_referenced_at);

CREATE TABLE request_texts (
  id BIGSERIAL PRIMARY KEY,
  request_id UUID NOT NULL,
  employee_id BIGINT NOT NULL,
  session_id BIGINT,
  started_at TIMESTAMPTZ NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('user','assistant')),
  text TEXT NOT NULL
);
CREATE INDEX request_texts_trgm ON request_texts USING gin (text public.gin_trgm_ops);
CREATE INDEX request_texts_emp_time ON request_texts(employee_id, started_at);
CREATE INDEX request_texts_request ON request_texts(request_id);

CREATE TABLE account_signals (
  id BIGSERIAL PRIMARY KEY,
  account_id BIGINT NOT NULL REFERENCES upstream_accounts(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  detail JSONB NOT NULL DEFAULT '{}',
  at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ,
  notified_at TIMESTAMPTZ
);
CREATE INDEX account_signals_open ON account_signals(account_id, kind, at DESC);

CREATE TABLE quota_snapshots (
  account_id BIGINT NOT NULL REFERENCES upstream_accounts(id) ON DELETE CASCADE,
  at TIMESTAMPTZ NOT NULL,
  primary_used_pct REAL,
  primary_window_minutes INT,
  primary_reset_at TIMESTAMPTZ,
  secondary_used_pct REAL,
  secondary_window_minutes INT,
  secondary_reset_at TIMESTAMPTZ,
  PRIMARY KEY (account_id, at)
);

CREATE TABLE daily_reports (
  id BIGSERIAL PRIMARY KEY,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  date DATE NOT NULL,
  stats JSONB NOT NULL,
  summary_md TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  pushed_at TIMESTAMPTZ,
  UNIQUE (employee_id, date)
);

CREATE TABLE audit_logs (
  id BIGSERIAL PRIMARY KEY,
  actor_employee_id BIGINT,
  action TEXT NOT NULL,
  target_type TEXT NOT NULL DEFAULT '',
  target_id TEXT NOT NULL DEFAULT '',
  detail JSONB NOT NULL DEFAULT '{}',
  ip TEXT NOT NULL DEFAULT '',
  at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_at ON audit_logs(at DESC);

-- +goose StatementBegin
CREATE FUNCTION audit_logs_immutable() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'audit_logs is append-only';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER audit_logs_no_change BEFORE UPDATE OR DELETE ON audit_logs
  FOR EACH ROW EXECUTE FUNCTION audit_logs_immutable();

CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE web_sessions (
  id TEXT PRIMARY KEY,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  csrf_token TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE observed_clients (
  user_agent TEXT PRIMARY KEY,
  originator TEXT NOT NULL DEFAULT '',
  version TEXT NOT NULL DEFAULT '',
  first_seen TIMESTAMPTZ NOT NULL,
  last_seen TIMESTAMPTZ NOT NULL,
  hits BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE oauth_states (
  state TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS oauth_states, observed_clients, web_sessions, settings,
  audit_logs, daily_reports, quota_snapshots, account_signals, request_texts,
  blobs, requests, sessions, api_keys, account_onboardings, upstream_accounts,
  ip_groups, employee_departments, departments, employees CASCADE;
DROP FUNCTION IF EXISTS audit_logs_immutable();

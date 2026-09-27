-- +goose Up
-- Key-sharing / resale risk controls: device + network registry, risk events,
-- employee-level freeze, versioned usage notice, per-request client IP/device.

ALTER TABLE employees ADD COLUMN frozen_at TIMESTAMPTZ;
ALTER TABLE employees ADD COLUMN frozen_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE employees ADD COLUMN notice_ack_version INT NOT NULL DEFAULT 0;
-- Everyone who acknowledged before this migration acknowledged version 1.
UPDATE employees SET notice_ack_version = 1 WHERE notice_ack_at IS NOT NULL;

CREATE TABLE client_devices (
  id BIGSERIAL PRIMARY KEY,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  device_key TEXT NOT NULL,
  kind TEXT NOT NULL,                       -- codex_install | ua
  label TEXT NOT NULL DEFAULT '',
  client_kind TEXT NOT NULL DEFAULT '',     -- codex | other
  user_agent TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending','trusted','blocked','expired')),
  first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  first_ip TEXT NOT NULL DEFAULT '',
  last_ip TEXT NOT NULL DEFAULT '',
  requests BIGINT NOT NULL DEFAULT 0,
  confirmed_at TIMESTAMPTZ,
  confirmed_by BIGINT,
  notified_at TIMESTAMPTZ,
  UNIQUE (employee_id, device_key)
);
CREATE INDEX client_devices_emp ON client_devices(employee_id, last_seen DESC);

CREATE TABLE client_networks (
  id BIGSERIAL PRIMARY KEY,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  net_key TEXT NOT NULL,                    -- IPv4 /24 or IPv6 /48
  ip_sample TEXT NOT NULL DEFAULT '',
  country TEXT NOT NULL DEFAULT '',
  province TEXT NOT NULL DEFAULT '',
  city TEXT NOT NULL DEFAULT '',
  isp TEXT NOT NULL DEFAULT '',
  datacenter BOOLEAN NOT NULL DEFAULT FALSE,
  first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  requests BIGINT NOT NULL DEFAULT 0,
  UNIQUE (employee_id, net_key)
);
CREATE INDEX client_networks_emp ON client_networks(employee_id, last_seen DESC);

CREATE TABLE risk_events (
  id BIGSERIAL PRIMARY KEY,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  rule TEXT NOT NULL,
  severity TEXT NOT NULL CHECK (severity IN ('info','warn','high')),
  detail JSONB NOT NULL DEFAULT '{}',
  device_id BIGINT REFERENCES client_devices(id) ON DELETE SET NULL,
  network_id BIGINT REFERENCES client_networks(id) ON DELETE SET NULL,
  at TIMESTAMPTZ NOT NULL DEFAULT now(),
  notified_at TIMESTAMPTZ,
  resolved_at TIMESTAMPTZ,
  resolved_by BIGINT
);
CREATE INDEX risk_events_open ON risk_events(resolved_at, at DESC);
CREATE INDEX risk_events_emp ON risk_events(employee_id, at DESC);

ALTER TABLE requests ADD COLUMN client_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE requests ADD COLUMN device_id BIGINT;

-- +goose Down
ALTER TABLE requests DROP COLUMN IF EXISTS device_id;
ALTER TABLE requests DROP COLUMN IF EXISTS client_ip;
DROP TABLE IF EXISTS risk_events, client_networks, client_devices;
ALTER TABLE employees DROP COLUMN IF EXISTS notice_ack_version;
ALTER TABLE employees DROP COLUMN IF EXISTS frozen_reason;
ALTER TABLE employees DROP COLUMN IF EXISTS frozen_at;

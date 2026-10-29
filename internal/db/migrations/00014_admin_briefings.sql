-- +goose Up
-- The weekly briefing each super admin receives on Fridays, built from the
-- week's daily work summaries. Each admin has their own prompt (focus) and
-- on/off switch; prompts and reports are personal.
CREATE TABLE admin_briefing_prefs (
  employee_id BIGINT PRIMARY KEY REFERENCES employees(id) ON DELETE CASCADE,
  enabled BOOLEAN NOT NULL DEFAULT true,
  prompt TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per admin per week: the latest generated briefing and how the run
-- went (test = generated from the portal, sent = pushed to Feishu).
CREATE TABLE admin_briefings (
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  week_start DATE NOT NULL,
  content_md TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','test','sent','failed')),
  error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  generated_at TIMESTAMPTZ,
  sent_at TIMESTAMPTZ,
  PRIMARY KEY (employee_id, week_start)
);

-- +goose Down
DROP TABLE admin_briefings;
DROP TABLE admin_briefing_prefs;

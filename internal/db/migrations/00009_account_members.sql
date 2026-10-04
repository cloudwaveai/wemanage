-- +goose Up
-- An employee can have several accounts and an account can be shared by a few
-- employees (the cap is a setting). The quota-reminder state moves to the
-- membership, since it is about one account's windows.
CREATE TABLE account_members (
  account_id BIGINT NOT NULL REFERENCES upstream_accounts(id) ON DELETE CASCADE,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  alerted_primary TIMESTAMPTZ,
  alerted_secondary TIMESTAMPTZ,
  exhausted_primary TIMESTAMPTZ,
  exhausted_secondary TIMESTAMPTZ,
  PRIMARY KEY (account_id, employee_id)
);
CREATE INDEX account_members_employee ON account_members(employee_id);

INSERT INTO account_members (account_id, employee_id, alerted_primary, alerted_secondary, exhausted_primary, exhausted_secondary)
SELECT a.id, a.employee_id, e.quota_alerted_primary, e.quota_alerted_secondary, e.quota_exhausted_primary, e.quota_exhausted_secondary
FROM upstream_accounts a JOIN employees e ON e.id = a.employee_id;

ALTER TABLE upstream_accounts DROP COLUMN employee_id;
ALTER TABLE employees
  DROP COLUMN quota_alerted_primary,
  DROP COLUMN quota_alerted_secondary,
  DROP COLUMN quota_exhausted_primary,
  DROP COLUMN quota_exhausted_secondary;

-- Per-account policy set by administrators: the models the account may be
-- used with (NULL: all of them), and whether clients other than Codex
-- (OpenCode, Command Code, …) may use it.
ALTER TABLE upstream_accounts
  ADD COLUMN allowed_models TEXT[],
  ADD COLUMN allow_third_party BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE upstream_accounts
  DROP COLUMN allowed_models,
  DROP COLUMN allow_third_party;
ALTER TABLE employees
  ADD COLUMN quota_alerted_primary TIMESTAMPTZ,
  ADD COLUMN quota_alerted_secondary TIMESTAMPTZ,
  ADD COLUMN quota_exhausted_primary TIMESTAMPTZ,
  ADD COLUMN quota_exhausted_secondary TIMESTAMPTZ;
ALTER TABLE upstream_accounts ADD COLUMN employee_id BIGINT UNIQUE REFERENCES employees(id) ON DELETE SET NULL;
-- Back to one account per employee: each account keeps its earliest member,
-- each employee their earliest account.
UPDATE upstream_accounts a SET employee_id = m.employee_id
FROM (
  SELECT DISTINCT ON (employee_id) account_id, employee_id
  FROM (SELECT DISTINCT ON (account_id) account_id, employee_id, created_at FROM account_members ORDER BY account_id, created_at) firsts
  ORDER BY employee_id, created_at
) m
WHERE a.id = m.account_id;
DROP TABLE account_members;

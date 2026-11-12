-- Additive migration. Legacy approvals remain readable and are never rewritten
-- as executor evidence by model or human decisions.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS execution_control_version BIGINT NOT NULL DEFAULT 0;
CREATE OR REPLACE FUNCTION bump_execution_control_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.paused IS DISTINCT FROM OLD.paused OR NEW.status IS DISTINCT FROM OLD.status THEN
        NEW.execution_control_version := OLD.execution_control_version + 1;
    END IF;
    RETURN NEW;
END $$;
DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgname='task_execution_control_version') THEN
        CREATE TRIGGER task_execution_control_version BEFORE UPDATE OF paused,status ON tasks
        FOR EACH ROW EXECUTE FUNCTION bump_execution_control_version();
    END IF;
END $$;
CREATE TABLE IF NOT EXISTS task_execution_policies (
    task_id BIGINT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    mode TEXT NOT NULL DEFAULT 'legacy' CHECK(mode IN ('legacy','observe','auto'))
);
CREATE TABLE IF NOT EXISTS tool_execution_receipts (
    approval_id BIGINT PRIMARY KEY REFERENCES intercept_pending(id) ON DELETE CASCADE,
    task_id TEXT NOT NULL,
    intent_id BIGINT NOT NULL DEFAULT 0,
    run_id TEXT NOT NULL,
    tool_use_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    working_dir TEXT NOT NULL DEFAULT '',
    input_digest TEXT NOT NULL,
    read_only BOOLEAN NOT NULL DEFAULT false,
    state TEXT NOT NULL DEFAULT 'prepared' CHECK(state IN ('prepared','running','succeeded','failed','canceled','not_started','unknown')),
    result_kind TEXT NOT NULL DEFAULT '',
    process JSONB NOT NULL DEFAULT '{}',
    output TEXT NOT NULL DEFAULT '',
    output_truncated BOOLEAN NOT NULL DEFAULT false,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE tool_execution_receipts ADD COLUMN IF NOT EXISTS result_kind TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_execution_task_intent ON tool_execution_receipts(task_id,intent_id);
CREATE TABLE IF NOT EXISTS tool_execution_events (
    id BIGSERIAL PRIMARY KEY,
    approval_id BIGINT NOT NULL REFERENCES intercept_pending(id) ON DELETE CASCADE,
    event_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(approval_id,event_key)
);
CREATE TABLE IF NOT EXISTS execution_recoveries (
    approval_id BIGINT PRIMARY KEY REFERENCES intercept_pending(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','checking','resolved','waiting_input','parked')),
    effect_state TEXT NOT NULL DEFAULT 'indeterminate' CHECK(effect_state IN ('indeterminate','achieved','partial','not_achieved','not_applicable')),
    decision JSONB NOT NULL DEFAULT '{}',
    version BIGINT NOT NULL DEFAULT 1,
    claim_token TEXT NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_execution_recovery_due ON execution_recoveries(next_check_at) WHERE state IN ('pending','checking');
CREATE TABLE IF NOT EXISTS execution_recovery_decisions (
    id BIGSERIAL PRIMARY KEY,
    approval_id BIGINT NOT NULL REFERENCES intercept_pending(id) ON DELETE CASCADE,
    source TEXT NOT NULL,
    evidence_version BIGINT NOT NULL,
    decision JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

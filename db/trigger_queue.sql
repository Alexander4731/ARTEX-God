CREATE TABLE IF NOT EXISTS trigger_runs (
    id BIGSERIAL PRIMARY KEY,
    trigger_id BIGINT REFERENCES agent_triggers(id) ON DELETE SET NULL,
    agent_key TEXT NOT NULL,
    event_kind TEXT NOT NULL,
    task_id BIGINT NOT NULL DEFAULT 0,
    title TEXT NOT NULL,
    message TEXT NOT NULL,
    task_description TEXT NOT NULL DEFAULT '',
    task_goal TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','queued','running','done','failed','interrupted')),
    conversation_id BIGINT REFERENCES conversations(id) ON DELETE SET NULL,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_trigger_runs_pending ON trigger_runs(id) WHERE state='pending';

-- Capture the event and its enabled recipients in the source transaction. IDs
-- order the queue, but never decide which committed events have been consumed.
CREATE OR REPLACE FUNCTION capture_agent_trigger_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    event TEXT;
    task_row tasks%ROWTYPE;
    body TEXT := '';
    tool_name TEXT := '';
BEGIN
    IF TG_TABLE_NAME='tasks' THEN
        task_row := NEW;
        IF TG_OP='INSERT' THEN event := 'task_create';
        ELSIF NEW.status='timeout' AND OLD.status IS DISTINCT FROM NEW.status THEN event := 'task_timeout';
        ELSE RETURN NEW; END IF;
    ELSIF TG_TABLE_NAME='exploration_nodes' THEN
        IF TG_OP='INSERT' AND NEW.kind='finding' THEN
            event := 'finding'; body := COALESCE(NEW.payload->>'summary','');
        ELSIF NEW.kind='goal' AND NEW.state='met' THEN
            IF TG_OP='UPDATE' AND OLD.state='met' THEN RETURN NEW; END IF;
            event := 'goal_met'; body := COALESCE(NEW.payload->>'text','');
        ELSE RETURN NEW; END IF;
        SELECT * INTO task_row FROM tasks WHERE exploration_id=NEW.exploration_id;
    ELSE
        IF NEW.kind IS DISTINCT FROM 'tool_result' OR (NEW.tool='report_finding' AND NEW.is_error) THEN RETURN NEW; END IF;
        event := 'tool_call'; tool_name := COALESCE(NEW.tool,'');
        body := '工具: ' || tool_name || E'\n返回: ' || CASE WHEN NEW.is_error THEN '[error] ' ELSE '' END || left(COALESCE(NEW.detail,NEW.summary,''),1500);
        body := body || E'\n入参: ' || COALESCE((SELECT left(detail,1500) FROM activity WHERE exploration_id=NEW.exploration_id AND tool_use_id=NEW.tool_use_id AND kind='tool_use' ORDER BY id DESC LIMIT 1),'');
        SELECT * INTO task_row FROM tasks WHERE exploration_id=NEW.exploration_id;
    END IF;
    IF task_row.id IS NULL OR task_row.deleted_at IS NOT NULL THEN RETURN NEW; END IF;
    INSERT INTO trigger_runs(trigger_id,agent_key,event_kind,task_id,title,message,task_description,task_goal)
    SELECT tr.id,tr.agent_key,event,task_row.id,
      event || ' · task#' || task_row.id,
      CASE event WHEN 'finding' THEN tr.finding_message WHEN 'goal_met' THEN tr.goal_message
      WHEN 'task_timeout' THEN tr.task_timeout_message WHEN 'task_create' THEN tr.task_create_message
      ELSE tr.tool_call_message END || E'\n\n【触发事件: ' || event || E'】\n' || left(body,4000),
      left(COALESCE(task_row.description,''),200),left(COALESCE(task_row.goal,''),500)
    FROM agent_triggers tr WHERE tr.enabled AND
      ((event='finding' AND tr.on_finding) OR (event='goal_met' AND tr.on_goal_met) OR
       (event='task_timeout' AND tr.on_task_timeout) OR (event='task_create' AND tr.on_task_create) OR
       (event='tool_call' AND tr.on_tool_call AND EXISTS(SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(tr.tool_names,''),'[]')::jsonb) n WHERE lower(n)=lower(tool_name))));
    RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER capture_task_trigger AFTER INSERT OR UPDATE OF status ON tasks
FOR EACH ROW EXECUTE FUNCTION capture_agent_trigger_event();
CREATE OR REPLACE TRIGGER capture_node_trigger AFTER INSERT OR UPDATE OF state ON exploration_nodes
FOR EACH ROW EXECUTE FUNCTION capture_agent_trigger_event();
CREATE OR REPLACE TRIGGER capture_tool_trigger AFTER INSERT ON activity
FOR EACH ROW EXECUTE FUNCTION capture_agent_trigger_event();

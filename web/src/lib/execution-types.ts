export interface ExecutionRecovery {
  id: number;
  task_id: string;
  intent_id: number;
  tool_name: string;
  input: unknown;
  execution_state: string;
  output: string;
  output_truncated: boolean;
  process: { cancel_cause?: string; exit_code?: number; output_path?: string };
  recovery_state: string;
  effect_state: string;
  decision: { verdict?: { decision?: string; reason?: string }; source?: string };
  evidence_version: number;
  version: number;
  last_error: string;
  write_blocked: boolean;
}

ALTER TABLE approvals ADD COLUMN IF NOT EXISTS input_form jsonb;
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS input_form_digest text NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS input_values jsonb;
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS inputs_digest text NOT NULL DEFAULT '';

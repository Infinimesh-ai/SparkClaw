CREATE TABLE IF NOT EXISTS medicine_box_state (
 owner_id text PRIMARY KEY,
 epoch text NOT NULL,
 sequence bigint NOT NULL DEFAULT 0 CHECK (sequence >= 0),
 floor bigint NOT NULL DEFAULT 0 CHECK (floor >= 0 AND floor <= sequence),
 receipt_count integer NOT NULL DEFAULT 0 CHECK (receipt_count >= 0)
);
CREATE TABLE IF NOT EXISTS medicine_items (
 owner_id text NOT NULL REFERENCES medicine_box_state(owner_id),
 id text NOT NULL CHECK (id ~ '^med_[a-f0-9]{32}$'),
 name text NOT NULL,
 category text NOT NULL,
 quantity numeric(8,2) NOT NULL CHECK (quantity >= 0 AND quantity <= 100000),
 unit text NOT NULL,
 expires_on date,
 no_expiry boolean NOT NULL,
 location text NOT NULL DEFAULT '',
 usage text NOT NULL DEFAULT '',
 notes text NOT NULL DEFAULT '',
 version bigint NOT NULL CHECK (version > 0),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(owner_id,id),
 CHECK (NOT no_expiry OR expires_on IS NULL)
);
CREATE TABLE IF NOT EXISTS medicine_events (
 owner_id text NOT NULL REFERENCES medicine_box_state(owner_id),
 sequence bigint NOT NULL CHECK (sequence > 0),
 item_id text NOT NULL,
 deleted boolean NOT NULL,
 item jsonb NOT NULL,
 PRIMARY KEY(owner_id,sequence)
);
CREATE TABLE IF NOT EXISTS medicine_operation_receipts (
 owner_id text NOT NULL REFERENCES medicine_box_state(owner_id),
 operation_id text NOT NULL,
 kind text NOT NULL CHECK (kind IN ('batch','import')),
 receipt jsonb NOT NULL,
 PRIMARY KEY(owner_id,operation_id)
);

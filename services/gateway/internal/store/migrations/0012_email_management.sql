-- Independently addressable domain records. parent/related hold typed foreign
-- identities; domain commands validate their same-owner referential integrity.
CREATE TABLE email_management_records (
 owner_id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK (kind IN ('mailbox','mail','capture','representation','context','conversation','decision','concern','concern_link','target','dependency','reference','refresh','job','thread','sync','view','command','counter','summary')),
 id TEXT NOT NULL,
 parent TEXT NOT NULL DEFAULT '',
 related TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT '',
 search_text TEXT NOT NULL DEFAULT '',
 sort_key TEXT NOT NULL DEFAULT '',
 payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
 PRIMARY KEY(owner_id,kind,id)
);
CREATE INDEX email_management_parent ON email_management_records(owner_id,kind,parent,sort_key DESC,id DESC);
CREATE INDEX email_management_related ON email_management_records(owner_id,kind,related,sort_key DESC,id DESC);
CREATE INDEX email_management_state ON email_management_records(owner_id,kind,state,sort_key DESC,id DESC);
CREATE INDEX email_management_order ON email_management_records(owner_id,kind,sort_key DESC,id DESC);

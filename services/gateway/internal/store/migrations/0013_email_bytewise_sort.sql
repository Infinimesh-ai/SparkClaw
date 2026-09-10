-- Cursor keys and the inclusive due-time '~' sentinel use bytewise ordering,
-- independent of the database locale. Keep index ordering aligned with reads.
DROP INDEX email_management_parent;
DROP INDEX email_management_related;
DROP INDEX email_management_state;
DROP INDEX email_management_order;
CREATE INDEX email_management_parent ON email_management_records(owner_id,kind,parent,sort_key COLLATE "C" DESC,id COLLATE "C" DESC);
CREATE INDEX email_management_related ON email_management_records(owner_id,kind,related,sort_key COLLATE "C" DESC,id COLLATE "C" DESC);
CREATE INDEX email_management_state ON email_management_records(owner_id,kind,state,sort_key COLLATE "C" DESC,id COLLATE "C" DESC);
CREATE INDEX email_management_order ON email_management_records(owner_id,kind,sort_key COLLATE "C" DESC,id COLLATE "C" DESC);

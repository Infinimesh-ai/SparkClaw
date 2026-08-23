-- Materialize the browser-state defaults that the Go PostgreSQL read paths
-- used to inject per row. 0002 remapped only browser_login_blocks
-- status/schema_version/version, so legacy rows could still carry an empty
-- resume_tool, a JSON-null resume_args, or unnormalized browser_auth_records
-- identity fields (mirroring migrateLegacyBrowserLoginBlock and
-- migrateLegacyBrowserAuthRecord field by field). After this migration the
-- read paths return stored rows verbatim.

UPDATE browser_login_blocks SET resume_tool = 'browser.read'
  WHERE btrim(resume_tool, E' \t\n\r\f\013') = '';
UPDATE browser_login_blocks SET resume_args = '{}'::jsonb
  WHERE resume_args = 'null'::jsonb;
UPDATE browser_login_blocks SET schema_version = 2 WHERE schema_version <= 0;
UPDATE browser_login_blocks SET version = 1 WHERE version <= 0;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM browser_auth_records
    GROUP BY btrim(id, E' \t\n\r\f\013')
    HAVING count(*) > 1
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = '23514',
      MESSAGE = 'browser auth record normalization would merge distinct identifiers';
  END IF;
END
$$;

UPDATE browser_auth_records SET
  id = btrim(id, E' \t\n\r\f\013'),
  owner_id = CASE WHEN btrim(owner_id, E' \t\n\r\f\013') = ''
    THEN 'owner' ELSE btrim(owner_id, E' \t\n\r\f\013') END,
  browser_profile_id = CASE WHEN btrim(browser_profile_id, E' \t\n\r\f\013') = ''
    THEN 'default' ELSE btrim(browser_profile_id, E' \t\n\r\f\013') END,
  site_origin = lower(rtrim(btrim(site_origin, E' \t\n\r\f\013'), '/')),
  site_realm = btrim(site_realm, E' \t\n\r\f\013'),
  account_hint = lower(btrim(account_hint, E' \t\n\r\f\013')),
  auth_strategy = CASE WHEN btrim(auth_strategy, E' \t\n\r\f\013') = ''
    THEN 'session_restore' ELSE btrim(auth_strategy, E' \t\n\r\f\013') END,
  status = CASE WHEN btrim(status, E' \t\n\r\f\013') = ''
    THEN 'active' ELSE btrim(status, E' \t\n\r\f\013') END;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM browser_login_blocks
    WHERE btrim(resume_tool, E' \t\n\r\f\013') = ''
       OR resume_args = 'null'::jsonb
       OR schema_version <= 0
       OR version <= 0
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = '23514',
      MESSAGE = 'browser login block backfill left an unmaterialized resume default';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM browser_auth_records
    WHERE id <> btrim(id, E' \t\n\r\f\013')
       OR owner_id = '' OR owner_id <> btrim(owner_id, E' \t\n\r\f\013')
       OR browser_profile_id = '' OR browser_profile_id <> btrim(browser_profile_id, E' \t\n\r\f\013')
       OR site_origin <> lower(rtrim(btrim(site_origin, E' \t\n\r\f\013'), '/'))
       OR site_realm <> btrim(site_realm, E' \t\n\r\f\013')
       OR account_hint <> lower(btrim(account_hint, E' \t\n\r\f\013'))
       OR auth_strategy = '' OR auth_strategy <> btrim(auth_strategy, E' \t\n\r\f\013')
       OR status = '' OR status <> btrim(status, E' \t\n\r\f\013')
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = '23514',
      MESSAGE = 'browser auth record backfill left an unnormalized field';
  END IF;
END
$$;

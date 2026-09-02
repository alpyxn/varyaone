-- app_role.sql — re-runnable creation + privileges for the non-superuser
-- application role varyaone_app.
--
-- This is the same content as migration 000148, kept as a standalone script
-- because it must be applied in two situations where migrations do NOT re-run:
--   * after `pg_restore` (the backup engine dumps with --no-privileges, so table
--     grants are lost and the role may not exist on the target cluster);
--   * by deploy.sh after `migrate`, together with `ALTER ROLE varyaone_app LOGIN
--     PASSWORD ...`, to actually turn enforcement on.
--
-- Safe to run any number of times. It never sets LOGIN or a password.
-- TestDeployAppRoleScriptMatchesMigration keeps it in sync with migration 000148.

DO $$
BEGIN
  CREATE ROLE varyaone_app NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
EXCEPTION
  WHEN duplicate_object THEN NULL;
END
$$;

GRANT USAGE ON SCHEMA public TO varyaone_app;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO varyaone_app;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO varyaone_app;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO varyaone_app;

ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO varyaone_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO varyaone_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT EXECUTE ON FUNCTIONS TO varyaone_app;

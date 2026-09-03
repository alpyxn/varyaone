-- Demo mode marks a company as disposable showcase data.
--
-- The flag is inert in a normal installation: it defaults to false, no existing
-- query reads it, and nothing in the product changes because of it. Only a
-- deployment started with VARYAONE_DEMO_MODE=true acts on it, where it is the
-- single guard that lets the demo seeder create and - far more importantly -
-- purge a company. A purge that cannot see this flag must refuse to run, so the
-- flag is what keeps `varyaone demo reset` from ever touching real data.
ALTER TABLE companies ADD COLUMN is_demo boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN companies.is_demo IS
  'Disposable demo company: may be purged and reseeded by the demo tooling. Always false in a normal installation.';

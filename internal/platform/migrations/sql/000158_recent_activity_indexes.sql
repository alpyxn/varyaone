-- The home screen's recent-activity feed takes the newest rows from the party
-- ledger, stock movements and draft documents. Without an index on the order it
-- asks for, each source is scanned whole and sorted to hand back fifteen rows.
--
-- The leading column is the company, then the timestamp the feed orders by -
-- which is COALESCE(posted_at, created_at) for the ledger and for documents,
-- because posted_at is empty until a document is posted - then the id, so rows
-- sharing a timestamp have a fixed order.
--
-- These are the only indexes on these tables led by (company_id, time), so they
-- serve no other query and are pure write cost on three append-heavy tables.
-- They are built in the migration transaction, which holds a write lock on each
-- table while it runs; on an installation with a long history, take the usual
-- backup and expect the upgrade to pause writes for the build.
CREATE INDEX party_ledger_recent_activity_idx
    ON party_ledger_entries (company_id, COALESCE(posted_at, created_at) DESC, id DESC);

CREATE INDEX stock_movements_recent_activity_idx
    ON stock_movements (company_id, posted_at DESC, id DESC);

CREATE INDEX documents_recent_activity_draft_idx
    ON documents (company_id, COALESCE(posted_at, created_at) DESC, id DESC)
    WHERE status = 'DRAFT';

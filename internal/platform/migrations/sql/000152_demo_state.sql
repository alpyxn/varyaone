-- Coordination state for the shared demo company (see migration 000151).
--
-- The API server and the worker are separate processes, so the reset schedule
-- and the "a reset is running right now" flag have to live somewhere both can
-- see. This table deliberately carries no company_id: it must survive the purge
-- that deletes every company-scoped row, and it is not company data.
--
-- Inert on a normal installation: nothing reads it unless VARYAONE_DEMO_MODE is
-- on.
CREATE TABLE demo_state (
    singleton boolean DEFAULT true NOT NULL,
    status text DEFAULT 'READY' NOT NULL,
    last_reset_at timestamp with time zone,
    next_reset_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT demo_state_singleton_check CHECK (singleton),
    CONSTRAINT demo_state_status_check CHECK (status = ANY (ARRAY['READY'::text, 'RESETTING'::text]))
);

ALTER TABLE ONLY demo_state ADD CONSTRAINT demo_state_pkey PRIMARY KEY (singleton);

INSERT INTO demo_state (singleton) VALUES (true) ON CONFLICT DO NOTHING;

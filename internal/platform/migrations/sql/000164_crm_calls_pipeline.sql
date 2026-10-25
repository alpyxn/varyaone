-- CRM: arama listeleri, konuşma metinleri, satış fırsatları ve aşamaları.

CREATE TABLE crm_call_scripts (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id),
    name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 120),
    body text NOT NULL DEFAULT '' CHECK (char_length(body) <= 20000),
    is_active boolean NOT NULL DEFAULT true,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (company_id, id)
);

-- Bir arama listesi, sırayla aranacak adaylardır. Ulaşılamayan aday
-- retry_minutes sonra yeniden sıraya girer; max_attempts denemeden sonra
-- listeden düşer.
CREATE TABLE crm_call_lists (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id),
    name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 120),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
    script_id uuid,
    retry_minutes integer NOT NULL DEFAULT 120 CHECK (retry_minutes BETWEEN 5 AND 10080),
    max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts BETWEEN 1 AND 20),
    is_active boolean NOT NULL DEFAULT true,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, script_id) REFERENCES crm_call_scripts(company_id, id)
);

CREATE TABLE crm_call_list_items (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id),
    list_id uuid NOT NULL,
    lead_id uuid NOT NULL,
    position bigint NOT NULL,
    status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','CALLBACK','DONE','SKIPPED')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    last_outcome text,
    last_called_at timestamptz,
    claimed_by uuid,
    claimed_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, id),
    UNIQUE (company_id, list_id, lead_id),
    FOREIGN KEY (company_id, list_id) REFERENCES crm_call_lists(company_id, id),
    FOREIGN KEY (company_id, lead_id) REFERENCES crm_leads(company_id, id),
    FOREIGN KEY (claimed_by) REFERENCES users(id),
    CHECK ((claimed_by IS NULL) = (claimed_until IS NULL))
);

CREATE INDEX crm_call_list_items_queue_idx ON crm_call_list_items (company_id, list_id, status, available_at, position);
CREATE INDEX crm_call_list_items_lead_idx ON crm_call_list_items (company_id, lead_id);

CREATE TABLE crm_pipeline_stages (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id),
    name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 80),
    sort_order integer NOT NULL DEFAULT 0,
    probability integer NOT NULL DEFAULT 0 CHECK (probability BETWEEN 0 AND 100),
    kind text NOT NULL DEFAULT 'OPEN' CHECK (kind IN ('OPEN','WON','LOST')),
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (company_id, id),
    UNIQUE (company_id, name)
);

-- quote_id bilerek yabancı anahtar değildir: taslak teklifler silinebilir ve
-- bir fırsat bağlantısı bu silmeyi engellememelidir.
CREATE TABLE crm_opportunities (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id),
    title text NOT NULL CHECK (char_length(btrim(title)) BETWEEN 1 AND 200),
    lead_id uuid,
    party_id uuid,
    stage_id uuid NOT NULL,
    amount numeric(20,4) NOT NULL DEFAULT 0 CHECK (amount >= 0),
    currency character(3) NOT NULL DEFAULT 'TRY' CHECK (currency ~ '^[A-Z]{3}$'),
    expected_close_date date,
    owner_user_id uuid,
    notes text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 4000),
    lost_reason text NOT NULL DEFAULT '' CHECK (char_length(lost_reason) <= 500),
    quote_id uuid,
    closed_at timestamptz,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, lead_id) REFERENCES crm_leads(company_id, id),
    FOREIGN KEY (company_id, party_id) REFERENCES parties(company_id, id),
    FOREIGN KEY (company_id, stage_id) REFERENCES crm_pipeline_stages(company_id, id),
    FOREIGN KEY (company_id, owner_user_id) REFERENCES company_memberships(company_id, user_id),
    CHECK (num_nonnulls(lead_id, party_id) = 1)
);

CREATE INDEX crm_opportunities_stage_idx ON crm_opportunities (company_id, stage_id, updated_at DESC);
CREATE INDEX crm_opportunities_party_idx ON crm_opportunities (company_id, party_id) WHERE party_id IS NOT NULL;
CREATE INDEX crm_opportunities_lead_idx ON crm_opportunities (company_id, lead_id) WHERE lead_id IS NOT NULL;
CREATE INDEX crm_opportunities_quote_idx ON crm_opportunities (company_id, quote_id) WHERE quote_id IS NOT NULL;

ALTER TABLE crm_activities ADD COLUMN opportunity_id uuid;
ALTER TABLE crm_activities ADD COLUMN call_list_id uuid;
ALTER TABLE crm_activities ADD CONSTRAINT crm_activities_company_id_opportunity_id_fkey
    FOREIGN KEY (company_id, opportunity_id) REFERENCES crm_opportunities(company_id, id);
ALTER TABLE crm_activities ADD CONSTRAINT crm_activities_company_id_call_list_id_fkey
    FOREIGN KEY (company_id, call_list_id) REFERENCES crm_call_lists(company_id, id);
CREATE INDEX crm_activities_opportunity_idx ON crm_activities (company_id, opportunity_id, occurred_at DESC)
    WHERE opportunity_id IS NOT NULL;
CREATE INDEX crm_activities_reminders_idx ON crm_activities (company_id, assigned_user_id, due_at)
    WHERE kind = 'TASK' AND done_at IS NULL AND notified_at IS NULL;

DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['crm_call_scripts','crm_call_lists','crm_call_list_items','crm_pipeline_stages','crm_opportunities'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',t);
  EXECUTE format($p$CREATE POLICY company_isolation ON %I USING (
   NULLIF(current_setting('varyaone.company_id',true),'') IS NULL OR company_id::text=current_setting('varyaone.company_id',true))
   WITH CHECK (NULLIF(current_setting('varyaone.company_id',true),'') IS NULL OR company_id::text=current_setting('varyaone.company_id',true))$p$,t);
 END LOOP;
END $$;

-- Varsayılan aşamalar. Sonradan kurulan şirketler için servis aynı listeyi
-- ilk kullanımda ekler.
INSERT INTO crm_pipeline_stages (id, company_id, name, sort_order, probability, kind)
SELECT gen_random_uuid(), c.id, s.name, s.sort_order, s.probability, s.kind
FROM companies c
CROSS JOIN (VALUES
    ('Yeni', 10, 10, 'OPEN'),
    ('Görüşme', 20, 25, 'OPEN'),
    ('Teklif', 30, 50, 'OPEN'),
    ('Pazarlık', 40, 75, 'OPEN'),
    ('Kazanıldı', 90, 100, 'WON'),
    ('Kaybedildi', 100, 0, 'LOST')
) AS s(name, sort_order, probability, kind)
ON CONFLICT DO NOTHING;

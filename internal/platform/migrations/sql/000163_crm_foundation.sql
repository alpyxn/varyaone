-- CRM modülünün temeli: adaylar (henüz cari olmayan potansiyel müşteriler) ve
-- aday/cari üzerinde tutulan görüşme, not ve görev zaman çizelgesi.
--
-- Adaylar parties tablosunda tutulmaz: cari kartı müşteri ya da tedarikçi
-- olmak zorundadır ve muhasebe kayıtlarına bağlanır. Aday kazanılınca cari
-- kartı açılır ve converted_party_id ile bağlanır.

CREATE TABLE crm_leads (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id),
    display_name text NOT NULL CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 200),
    contact_name text NOT NULL DEFAULT '' CHECK (char_length(contact_name) <= 200),
    contact_title text NOT NULL DEFAULT '' CHECK (char_length(contact_title) <= 120),
    phone text NOT NULL DEFAULT '' CHECK (char_length(phone) <= 40),
    phone_normalized text NOT NULL DEFAULT '' CHECK (phone_normalized ~ '^[0-9]{0,15}$'),
    phone2 text NOT NULL DEFAULT '' CHECK (char_length(phone2) <= 40),
    email text NOT NULL DEFAULT '' CHECK (char_length(email) <= 254),
    city text NOT NULL DEFAULT '' CHECK (char_length(city) <= 80),
    district text NOT NULL DEFAULT '' CHECK (char_length(district) <= 80),
    sector text NOT NULL DEFAULT '' CHECK (char_length(sector) <= 120),
    source text NOT NULL DEFAULT '' CHECK (char_length(source) <= 120),
    notes text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 4000),
    status text NOT NULL DEFAULT 'NEW'
        CHECK (status IN ('NEW','CONTACTED','QUALIFIED','DISQUALIFIED','CONVERTED')),
    owner_user_id uuid,
    next_action_at timestamptz,
    do_not_call boolean NOT NULL DEFAULT false,
    do_not_call_reason text NOT NULL DEFAULT '' CHECK (char_length(do_not_call_reason) <= 500),
    do_not_call_at timestamptz,
    privacy_notice_at timestamptz,
    call_attempts integer NOT NULL DEFAULT 0 CHECK (call_attempts >= 0),
    last_call_at timestamptz,
    last_call_outcome text,
    converted_party_id uuid,
    converted_at timestamptz,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, owner_user_id) REFERENCES company_memberships(company_id, user_id),
    FOREIGN KEY (company_id, converted_party_id) REFERENCES parties(company_id, id),
    CHECK (do_not_call = (do_not_call_at IS NOT NULL)),
    CHECK ((status = 'CONVERTED') = (converted_party_id IS NOT NULL)),
    CHECK ((converted_party_id IS NULL) = (converted_at IS NULL))
);

CREATE INDEX crm_leads_company_status_idx ON crm_leads (company_id, status, updated_at DESC);
CREATE INDEX crm_leads_company_owner_idx ON crm_leads (company_id, owner_user_id);
CREATE INDEX crm_leads_company_phone_idx ON crm_leads (company_id, phone_normalized) WHERE phone_normalized <> '';
CREATE INDEX crm_leads_company_next_action_idx ON crm_leads (company_id, next_action_at) WHERE next_action_at IS NOT NULL;

CREATE TABLE crm_activities (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id),
    lead_id uuid,
    party_id uuid,
    kind text NOT NULL CHECK (kind IN ('CALL','MEETING','EMAIL','NOTE','TASK')),
    subject text NOT NULL DEFAULT '' CHECK (char_length(subject) <= 200),
    body text NOT NULL DEFAULT '' CHECK (char_length(body) <= 4000),
    call_outcome text CHECK (call_outcome IN ('NO_ANSWER','BUSY','WRONG_NUMBER','VOICEMAIL','CALLBACK',
        'NOT_INTERESTED','INTERESTED','MEETING_SET','DO_NOT_CALL')),
    duration_seconds integer CHECK (duration_seconds BETWEEN 0 AND 86400),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    due_at timestamptz,
    remind_minutes integer NOT NULL DEFAULT 0 CHECK (remind_minutes BETWEEN 0 AND 10080),
    notified_at timestamptz,
    done_at timestamptz,
    assigned_user_id uuid,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, lead_id) REFERENCES crm_leads(company_id, id),
    FOREIGN KEY (company_id, party_id) REFERENCES parties(company_id, id),
    FOREIGN KEY (company_id, assigned_user_id) REFERENCES company_memberships(company_id, user_id),
    CHECK (num_nonnulls(lead_id, party_id) = 1),
    CHECK ((kind = 'CALL') = (call_outcome IS NOT NULL)),
    CHECK (kind = 'TASK' OR (due_at IS NULL AND done_at IS NULL)),
    CHECK (kind <> 'TASK' OR (due_at IS NOT NULL AND char_length(btrim(subject)) >= 1))
);

CREATE INDEX crm_activities_lead_idx ON crm_activities (company_id, lead_id, occurred_at DESC) WHERE lead_id IS NOT NULL;
CREATE INDEX crm_activities_party_idx ON crm_activities (company_id, party_id, occurred_at DESC) WHERE party_id IS NOT NULL;
CREATE INDEX crm_activities_open_tasks_idx ON crm_activities (company_id, assigned_user_id, due_at)
    WHERE kind = 'TASK' AND done_at IS NULL;
CREATE INDEX crm_activities_calls_idx ON crm_activities (company_id, created_by, occurred_at)
    WHERE kind = 'CALL';

DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['crm_leads','crm_activities'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',t);
  EXECUTE format($p$CREATE POLICY company_isolation ON %I USING (
   NULLIF(current_setting('varyaone.company_id',true),'') IS NULL OR company_id::text=current_setting('varyaone.company_id',true))
   WITH CHECK (NULLIF(current_setting('varyaone.company_id',true),'') IS NULL OR company_id::text=current_setting('varyaone.company_id',true))$p$,t);
 END LOOP;
END $$;

INSERT INTO permissions (code, description) VALUES
    ('crm.read', 'CRM adaylarını ve görüşmelerini görüntüleme'),
    ('crm.edit', 'CRM adayı, görüşme ve görev kaydetme'),
    ('crm.manage', 'CRM kayıtlarını başka kullanıcılara atama ve yönetme'),
    ('crm.view_all', 'Bütün kullanıcıların CRM kayıtlarını görme');

-- Sistem rolü (Yönetici) her yetkiye sahiptir; yeni yetkiler de ona eklenir.
INSERT INTO role_permissions (company_id, role_id, permission_code)
SELECT r.company_id, r.id, p.code
FROM roles r
CROSS JOIN (VALUES ('crm.read'), ('crm.edit'), ('crm.manage'), ('crm.view_all')) AS p(code)
WHERE r.is_system
ON CONFLICT DO NOTHING;

-- Mevcut şirketlerde modül kapalı başlar; yönetici Ayarlar'dan açar.
INSERT INTO company_modules (company_id, module_code, enabled)
SELECT id, 'crm', false FROM companies
ON CONFLICT DO NOTHING;

export type EmailTemplateScope = 'GENERIC' | 'PAYROLL_PAYSLIP';

export type EmailTemplate = {
  id: string;
  code: string;
  name: string;
  scope: EmailTemplateScope;
  subject: string;
  body: string;
  description: string;
  is_system: boolean;
  is_active: boolean;
  created_at: string;
  version: number;
};

export type EmailTemplateInput = {
  code?: string;
  name: string;
  scope: EmailTemplateScope;
  subject: string;
  body: string;
  description?: string;
};

export type EmailComposerRecipient = {
  email: string;
  name: string;
  variables?: Record<string, string>;
};

export type EmailRecipientStatus = {
  email: string;
  name: string;
  status: 'ready' | 'missing' | 'invalid' | 'duplicate';
};

export type EmailPreviewResult = {
  subject: string;
  body: string;
  recipients: EmailRecipientStatus[];
  ready_count: number;
  sample_subject: string;
  sample_body: string;
};

export type EmailSendResult = {
  message_id?: string;
  status: string;
  sent: number;
  failed: number;
  skipped: number;
};

export const SCOPE_LABELS: Record<EmailTemplateScope, string> = {
  GENERIC: 'Genel',
  PAYROLL_PAYSLIP: 'Ücret pusulası'
};

/** Client-side {{key}} substitution mirroring internal/email.RenderText. */
export function renderVars(text: string, vars: Record<string, string> = {}): string {
  return text.replace(/\{\{\s*([a-zA-Z0-9_]+)\s*\}\}/g, (match, key) =>
    key in vars ? vars[key] : match
  );
}

import { api } from '$lib/api';
import type {
  EmailComposerRecipient,
  EmailPreviewResult,
  EmailSendResult,
  EmailTemplate,
  EmailTemplateInput
} from './types';

export const listEmailTemplates = (scope?: string, includeArchived = false) => {
  const qs = new URLSearchParams();
  if (scope) qs.set('scope', scope);
  if (includeArchived) qs.set('status', 'all');
  const suffix = qs.toString() ? `?${qs}` : '';
  return api<{ items: EmailTemplate[] }>(`/email-templates${suffix}`);
};

export const createEmailTemplate = (input: EmailTemplateInput) =>
  api<EmailTemplate>('/email-templates', { method: 'POST', body: JSON.stringify(input) });

export const updateEmailTemplate = (id: string, version: number, input: EmailTemplateInput) =>
  api<EmailTemplate>(`/email-templates/${id}`, {
    method: 'PATCH',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(input)
  });

export const setEmailTemplateActive = (id: string, active: boolean) =>
  api<EmailTemplate>(`/email-templates/${id}/status`, {
    method: 'POST',
    body: JSON.stringify({ active })
  });

type ComposePayload = {
  subject: string;
  body: string;
  template_id?: string;
  context_type?: string;
  context_id?: string;
  recipients: EmailComposerRecipient[];
};

export const previewEmail = (payload: ComposePayload) =>
  api<EmailPreviewResult>('/email/preview', { method: 'POST', body: JSON.stringify(payload) });

export const sendEmail = (payload: ComposePayload) =>
  api<EmailSendResult & { preview: EmailPreviewResult }>('/email/messages', {
    method: 'POST',
    body: JSON.stringify(payload)
  });

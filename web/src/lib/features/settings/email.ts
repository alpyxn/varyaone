import { api } from '$lib/api';

export type SMTPSettings = {
  host: string;
  port: number;
  security_mode: 'TLS' | 'STARTTLS';
  username: string;
  from_email: string;
  from_name: string;
  connect_timeout_seconds: number;
  has_password: boolean;
  version: number;
};

export type SMTPSettingsInput = {
  host: string;
  port: number;
  security_mode: string;
  username: string;
  password?: string | null;
  from_email: string;
  from_name: string;
  connect_timeout_seconds: number;
};

export type SMTPTestResult = {
  connected: boolean;
  authenticated: boolean;
  security_mode: string;
};

export const getEmailSettings = () => api<SMTPSettings>('/settings/email');

export const saveEmailSettings = (version: number, input: SMTPSettingsInput) =>
  api<SMTPSettings>('/settings/email', {
    method: 'PUT',
    headers: version ? { 'If-Match': `"${version}"` } : {},
    body: JSON.stringify(input)
  });

export const testEmailSettings = () =>
  api<SMTPTestResult>('/settings/email/test', { method: 'POST' });

import { redirect } from '@sveltejs/kit';
import { DEFAULT_REPORT_ID } from '$lib/features/reporting/registry';

export const load = () => {
  redirect(307, `/raporlar/${DEFAULT_REPORT_ID}`);
};

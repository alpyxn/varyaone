import type { Component } from 'svelte';
import { ArrowLeftRight, Boxes, FileText, ReceiptText, Wallet } from '@lucide/svelte';
import { commercialDocumentHref } from '$lib/features/commercial/types';
import { formatMoney, formatQuantity, formatRelative } from '$lib/design/formatters';
import type { RecentActivityEntry } from '$lib/api';

export type RecentActivityView = {
  icon: Component;
  title: string;
  subtitle: string;
  amount: string;
  when: string;
  href?: string;
};

const documentTypeLabels: Record<string, string> = {
  SALES_QUOTE: 'Satış teklifi',
  SALES_ORDER: 'Satış siparişi',
  SALES_DELIVERY: 'Satış irsaliyesi',
  SALES_DISPATCH: 'Satış irsaliyesi',
  SALES_INVOICE: 'Satış faturası',
  SALES_RETURN: 'Satış iadesi',
  SALES_RETURN_INVOICE: 'Satış iade faturası',
  PURCHASE_QUOTE: 'Alış teklifi',
  PURCHASE_ORDER: 'Alış siparişi',
  PURCHASE_DELIVERY: 'Alış irsaliyesi',
  PURCHASE_INVOICE: 'Alış faturası',
  PURCHASE_RETURN: 'Alış iadesi',
  PURCHASE_RETURN_INVOICE: 'Alış iade faturası'
};

const stockMovementLabels: Record<string, string> = {
  PURCHASE_RECEIPT: 'Mal kabul',
  SALES_DISPATCH: 'Satış sevkiyatı',
  SALES_RETURN: 'Satış iadesi',
  PURCHASE_RETURN: 'Alış iadesi',
  TRANSFER_OUT: 'Depo çıkışı',
  TRANSFER_IN: 'Depo girişi',
  COUNT_ADJUSTMENT: 'Sayım düzeltmesi',
  MANUAL_ADJUSTMENT: 'Manuel düzeltme',
  DAMAGE: 'Hasar',
  WASTE: 'Fire',
  RECONCILIATION: 'Mutabakat'
};

function documentHref(entry: RecentActivityEntry): string | undefined {
  const code = (entry.title_code ?? '').toUpperCase();
  const direction = code.startsWith('PURCHASE') ? 'purchases' : 'sales';
  return commercialDocumentHref(direction, code, entry.ref_id ?? entry.entry_id);
}

export function toRecentActivityView(entry: RecentActivityEntry): RecentActivityView {
  const when = formatRelative(entry.occurred_at);
  const currency = entry.currency || 'TRY';

  if (entry.kind === 'document') {
    const label = documentTypeLabels[(entry.title_code ?? '').toUpperCase()] ?? 'Belge';
    return {
      icon: FileText,
      title: entry.label ? `${label} · ${entry.label}` : label,
      subtitle: entry.party_name || 'Taslak',
      amount: entry.amount ? formatMoney(entry.amount, currency) : '',
      when,
      href: documentHref(entry)
    };
  }

  if (entry.kind === 'stock') {
    const label = stockMovementLabels[(entry.title_code ?? '').toUpperCase()] ?? 'Stok hareketi';
    const sign = entry.direction === 'OUT' ? '−' : '+';
    return {
      icon: Boxes,
      title: entry.label ? `${label} · ${entry.label}` : label,
      subtitle: entry.direction === 'OUT' ? 'Çıkış' : 'Giriş',
      amount: entry.amount ? `${sign}${formatQuantity(entry.amount)}` : '',
      when,
      href: '/stok/hareketler'
    };
  }

  // ledger
  const isPayment = entry.ref_type === 'finance_payment';
  const isTransfer = entry.ref_type === 'finance_party_transfer';
  const icon = isPayment ? Wallet : isTransfer ? ArrowLeftRight : ReceiptText;
  const negative = (entry.amount ?? '').trim().startsWith('-');
  return {
    icon,
    title: entry.label || 'Cari hareket',
    subtitle: entry.party_name || '—',
    amount: entry.amount
      ? formatMoney(negative ? entry.amount.replace('-', '') : entry.amount, currency)
      : '',
    when,
    href: '/cari/hareketler'
  };
}

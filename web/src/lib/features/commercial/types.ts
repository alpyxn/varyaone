export type CommercialDirection = 'sales' | 'purchases';
export type CommercialResource = 'quotes' | 'orders' | 'dispatches' | 'invoices' | 'returns';
export type CommercialLineType = 'PRODUCT' | 'SERVICE';
export type CommercialDocumentReference = {
  id: string;
  document_no?: string;
  document_type_code?: string;
  document_type?: string;
  kind?: string;
  resource?: string;
  relation_type?: string;
  direction?: string;
  lifecycle_status?: string;
  status?: string;
  party_name?: string;
  supplier_name?: string;
};
export type CommercialStatusAxis =
  'lifecycle_status' | 'fulfillment_status' | 'invoicing_status' | 'payment_status';

/** Wire models for the purchase line contracts exposed by the typed API. */
export type PurchaseOrderLineModel = {
  id?: string;
  line_type: CommercialLineType;
  product_id: string;
  variant_id?: string;
  warehouse_id?: string | null;
  unit_code: string;
  ordered_quantity: string;
  base_quantity?: string;
  conversion_factor?: string;
  unit_price: string;
  currency?: string;
};

export type PurchaseInvoiceLineModel = {
  id?: string;
  line_type: CommercialLineType;
  purchase_order_line_id?: string;
  goods_receipt_line_id?: string;
  product_id: string;
  variant_id?: string;
  warehouse_id?: string | null;
  unit_code: string;
  quantity: string;
  unit_price: string;
  gross_amount: string;
  discount_amount: string;
  tax_base: string;
  tax_amount: string;
  withholding_amount: string;
  payable_amount: string;
};

export type CommercialResourceConfig = {
  direction: CommercialDirection;
  resource: CommercialResource;
  title: string;
  listTitle: string;
  subtitle?: string;
  permission: string;
  managePermission: string;
  /** Permission to prepare/edit a draft. Equals managePermission for quotes and
   *  orders; a separate ".draft" permission for the posting documents. */
  draftPermission: string;
  endpoint: string;
  isDraft: boolean;
  primaryLabel: string;
};

const commercialRouteSlugs: Record<CommercialResource, string> = {
  quotes: 'teklifler',
  orders: 'siparisler',
  dispatches: 'irsaliyeler',
  invoices: 'faturalar',
  returns: 'iadeler'
};

const salesConfig: Record<
  CommercialResource,
  Omit<CommercialResourceConfig, 'direction' | 'resource'>
> = {
  quotes: {
    title: 'Satış Teklifi',
    listTitle: 'Satış Teklifleri',
    permission: 'sales.quote.read',
    managePermission: 'sales.quote.manage',
    draftPermission: 'sales.quote.manage',
    endpoint: '/sales/quotes',
    isDraft: true,
    primaryLabel: 'Yeni Teklif'
  },
  orders: {
    title: 'Satış Siparişi',
    listTitle: 'Satış Siparişleri',
    permission: 'sales.order.read',
    managePermission: 'sales.order.manage',
    draftPermission: 'sales.order.manage',
    endpoint: '/sales/orders',
    isDraft: true,
    primaryLabel: 'Yeni Sipariş'
  },
  dispatches: {
    title: 'Satış İrsaliyesi',
    listTitle: 'Satış İrsaliyeleri',
    permission: 'sales.dispatch.read',
    managePermission: 'sales.dispatch.post',
    draftPermission: 'sales.dispatch.draft',
    endpoint: '/sales/dispatches',
    isDraft: true,
    primaryLabel: 'Yeni İrsaliye'
  },
  invoices: {
    title: 'Satış Faturası',
    listTitle: 'Satış Faturaları',
    permission: 'sales.invoice.read',
    managePermission: 'sales.invoice.post',
    draftPermission: 'sales.invoice.draft',
    endpoint: '/sales/invoices',
    isDraft: true,
    primaryLabel: 'Yeni Fatura'
  },
  returns: {
    title: 'Satış İadesi',
    listTitle: 'Satış İadeleri',
    permission: 'sales.return.read',
    managePermission: 'sales.return.post',
    draftPermission: 'sales.return.draft',
    endpoint: '/sales/returns',
    isDraft: true,
    primaryLabel: 'Yeni İade'
  }
};

const purchaseConfig: Record<
  CommercialResource,
  Omit<CommercialResourceConfig, 'direction' | 'resource'>
> = {
  quotes: {
    title: 'Alış Teklifi',
    listTitle: 'Alış Teklifleri',
    subtitle: 'Alış teklifleri bu çalışma alanında kullanılmıyor',
    permission: 'purchase.order.read',
    managePermission: 'purchase.order.manage',
    draftPermission: 'purchase.order.manage',
    endpoint: '/purchases/quotes',
    isDraft: true,
    primaryLabel: 'Yeni Teklif'
  },
  orders: {
    title: 'Alış Siparişi',
    listTitle: 'Alış Siparişleri',
    permission: 'purchase.order.read',
    managePermission: 'purchase.order.manage',
    draftPermission: 'purchase.order.manage',
    endpoint: '/purchases/orders',
    isDraft: true,
    primaryLabel: 'Yeni Sipariş'
  },
  dispatches: {
    title: 'Alış İrsaliyesi',
    listTitle: 'Alış İrsaliyeleri',
    permission: 'purchase.receipt.post',
    managePermission: 'purchase.receipt.post',
    draftPermission: 'purchase.receipt.draft',
    endpoint: '/purchases/dispatches',
    isDraft: true,
    primaryLabel: 'Yeni İrsaliye'
  },
  invoices: {
    title: 'Alış Faturası',
    listTitle: 'Alış Faturaları',
    permission: 'purchase.invoice.post',
    managePermission: 'purchase.invoice.post',
    draftPermission: 'purchase.invoice.draft',
    endpoint: '/purchases/invoices',
    isDraft: true,
    primaryLabel: 'Yeni Fatura'
  },
  returns: {
    title: 'Alış İadesi',
    listTitle: 'Alış İadeleri',
    permission: 'purchase.return.post',
    managePermission: 'purchase.return.post',
    draftPermission: 'purchase.return.draft',
    endpoint: '/purchases/returns',
    isDraft: true,
    primaryLabel: 'Yeni İade'
  }
};

export function commercialResource(value: string | undefined): CommercialResource | undefined {
  const aliases: Record<string, CommercialResource> = {
    quotes: 'quotes',
    teklifler: 'quotes',
    orders: 'orders',
    siparisler: 'orders',
    dispatches: 'dispatches',
    irsaliyeler: 'dispatches',
    invoices: 'invoices',
    faturalar: 'invoices',
    returns: 'returns',
    iadeler: 'returns'
  };
  return value ? aliases[value.trim().toLowerCase()] : undefined;
}

export function commercialConfig(
  direction: CommercialDirection,
  resource: string | undefined
): CommercialResourceConfig | undefined {
  const normalized = commercialResource(resource);
  if (!normalized) return undefined;
  const source = direction === 'sales' ? salesConfig : purchaseConfig;
  // Alış teklifi route'u backend'de deliberately yoktur; config yalnızca
  // type-safe route çözümlemesi için tutulur ve liste sayfasında engellenir.
  if (direction === 'purchases' && normalized === 'quotes') return undefined;
  return { direction, resource: normalized, ...source[normalized] };
}

export function commercialPath(
  direction: CommercialDirection,
  resource: string | undefined
): string {
  const normalized = commercialResource(resource);
  if (!normalized) return '';
  return `/${direction === 'sales' ? 'satis' : 'alis'}/${commercialRouteSlugs[normalized]}`;
}

export function commercialResourceFromReference(value: unknown): CommercialResource | undefined {
  const candidate = value && typeof value === 'object' ? (value as Record<string, unknown>) : {};
  const values = [
    candidate.resource,
    candidate.kind,
    candidate.document_type_code,
    candidate.document_type
  ];
  for (const raw of values) {
    const normalized = commercialResource(typeof raw === 'string' ? raw : undefined);
    if (normalized) return normalized;
    const code = String(raw ?? '')
      .trim()
      .toUpperCase();
    if (code === 'QUOTE' || code.endsWith('_QUOTE')) return 'quotes';
    if (code === 'ORDER' || code.endsWith('_ORDER')) return 'orders';
    if (
      code === 'DISPATCH' ||
      code === 'RECEIPT' ||
      code === 'DELIVERY' ||
      code === 'GOODS_RECEIPT' ||
      code.endsWith('_DISPATCH') ||
      code.endsWith('_RECEIPT') ||
      code.endsWith('_DELIVERY')
    )
      return 'dispatches';
    if (code === 'INVOICE' || code.endsWith('_INVOICE')) return 'invoices';
    if (code === 'RETURN' || code.endsWith('_RETURN')) return 'returns';
  }
  return undefined;
}

export function commercialDocumentHref(
  direction: CommercialDirection,
  resource: unknown,
  id: unknown
): string | undefined {
  const value = String(id ?? '').trim();
  const normalized = commercialResourceFromReference({ resource });
  if (!value || !normalized) return undefined;
  return `${commercialPath(direction, normalized)}/${encodeURIComponent(value)}`;
}

export function documentStatusLabel(status: unknown): string {
  const labels: Record<string, string> = {
    DRAFT: 'Taslak',
    SENT: 'Gönderildi',
    ACCEPTED: 'Kabul edildi',
    REJECTED: 'Reddedildi',
    EXPIRED: 'Süresi doldu',
    OPEN: 'Açık',
    CONFIRMED: 'Açık',
    PARTIALLY_FULFILLED: 'Kısmi karşılandı',
    FULFILLED: 'Tam karşılandı',
    POSTED: 'Sonlandırıldı',
    FINALIZED: 'Sonlandırıldı',
    CANCELLED: 'İptal'
  };
  const value = String(status ?? '').toUpperCase();
  return labels[value] ?? 'Bilinmiyor';
}

export const commercialStatusLabels: Record<CommercialStatusAxis, Record<string, string>> = {
  lifecycle_status: {
    DRAFT: 'Taslak',
    SENT: 'Gönderildi',
    ACCEPTED: 'Kabul edildi',
    REJECTED: 'Reddedildi',
    EXPIRED: 'Süresi doldu',
    OPEN: 'Açık',
    CONFIRMED: 'Açık',
    POSTED: 'Sonlandırıldı',
    FINALIZED: 'Sonlandırıldı',
    CANCELLED: 'İptal'
  },
  fulfillment_status: {
    UNFULFILLED: 'Karşılanmadı',
    PARTIALLY_FULFILLED: 'Kısmi karşılandı',
    FULFILLED: 'Tam karşılandı'
  },
  invoicing_status: {
    UNINVOICED: 'Faturalanmadı',
    PARTIALLY_INVOICED: 'Kısmi faturalandı',
    INVOICED: 'Faturalandı'
  },
  payment_status: {
    UNPAID: 'Ödenmedi',
    PARTIALLY_PAID: 'Kısmi ödendi',
    PAID: 'Ödendi'
  }
};

export function commercialStatusOptions(resource: CommercialResource, axis: CommercialStatusAxis) {
  const labels = commercialStatusLabels[axis];
  const values: Record<CommercialResource, Record<CommercialStatusAxis, string[]>> = {
    quotes: {
      lifecycle_status: ['DRAFT', 'SENT', 'ACCEPTED', 'REJECTED', 'EXPIRED', 'CANCELLED'],
      fulfillment_status: [],
      invoicing_status: [],
      payment_status: []
    },
    orders: {
      lifecycle_status: ['DRAFT', 'OPEN', 'CANCELLED'],
      fulfillment_status: ['UNFULFILLED', 'PARTIALLY_FULFILLED', 'FULFILLED'],
      invoicing_status: ['UNINVOICED', 'PARTIALLY_INVOICED', 'INVOICED'],
      payment_status: []
    },
    dispatches: {
      lifecycle_status: ['DRAFT', 'FINALIZED', 'CANCELLED'],
      fulfillment_status: [],
      invoicing_status: ['UNINVOICED', 'PARTIALLY_INVOICED', 'INVOICED'],
      payment_status: []
    },
    invoices: {
      lifecycle_status: ['DRAFT', 'FINALIZED', 'CANCELLED'],
      fulfillment_status: [],
      invoicing_status: [],
      payment_status: ['UNPAID', 'PARTIALLY_PAID', 'PAID']
    },
    returns: {
      lifecycle_status: ['DRAFT', 'FINALIZED', 'CANCELLED'],
      fulfillment_status: [],
      invoicing_status: [],
      payment_status: []
    }
  };
  return values[resource][axis].map((value) => ({ value, label: labels[value] ?? value }));
}

export function documentStatusTone(
  status: unknown
): 'neutral' | 'success' | 'warning' | 'danger' | 'info' {
  switch (String(status ?? '').toUpperCase()) {
    case 'POSTED':
    case 'FINALIZED':
    case 'ACCEPTED':
    case 'FULFILLED':
    case 'PAID':
    case 'INVOICED':
      return 'success';
    case 'SENT':
    case 'CONFIRMED':
    case 'OPEN':
      return 'info';
    case 'PARTIALLY_FULFILLED':
    case 'PARTIALLY_INVOICED':
    case 'PARTIALLY_PAID':
      return 'warning';
    case 'CANCELLED':
    case 'REJECTED':
    case 'UNPAID':
      return 'danger';
    default:
      return 'neutral';
  }
}

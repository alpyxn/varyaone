const enumLabels: Record<string, Record<string, string>> = {
  direction: {
    IN: 'Giriş',
    OUT: 'Çıkış'
  },
  movement_type: {
    PURCHASE_RECEIPT: 'Alış / Mal kabul',
    SALES_DISPATCH: 'Satış / Sevk',
    SALES_RETURN: 'Satış iadesi',
    PURCHASE_RETURN: 'Alış iadesi',
    TRANSFER_OUT: 'Depo transfer çıkışı',
    TRANSFER_IN: 'Depo transfer girişi',
    COUNT_ADJUSTMENT: 'Sayım düzeltmesi',
    MANUAL_ADJUSTMENT: 'Manuel stok düzeltmesi',
    DAMAGE: 'Hasar',
    WASTE: 'Fire / Zayi',
    RECONCILIATION: 'Mutabakat / düzeltme'
  },
  reason_code: {
    PURCHASE_RECEIPT: 'Alış / Mal kabul',
    SALES_DISPATCH: 'Satış / Sevk',
    SALES_RETURN: 'Satış iadesi',
    PURCHASE_RETURN: 'Alış iadesi',
    OPENING: 'Açılış',
    CORRECTION: 'Düzeltme',
    ADJUSTMENT: 'Stok düzeltmesi',
    PROMOTION: 'Promosyon',
    DAMAGE: 'Hasar',
    WASTE: 'Fire / Zayi',
    SAMPLE: 'Numune',
    INTERNAL_USE: 'Dahili kullanım',
    COUNT: 'Sayım',
    TRANSFER: 'Depo transferi',
    REVERSAL: 'Ters kayıt',
    OTHER: 'Diğer'
  },
  entry_type: {
    COLLECTION: 'Tahsilat',
    PAYMENT: 'Ödeme',
    INVOICE: 'Fatura',
    RECEIVABLE: 'Fatura (alacak)',
    PAYABLE: 'Fatura (borç)',
    MANUAL_ENTRY: 'Manuel kayıt',
    REVERSAL: 'Tersine İşlem',
    DEBIT_TRANSFER: 'Borç transferi',
    CREDIT_TRANSFER: 'Alacak transferi',
    OPENING_BALANCE: 'Açılış bakiyesi',
    CARRY_FORWARD: 'Devir'
  },
  movement_kind: {
    OPENING_BALANCE: 'Açılış bakiyesi',
    MANUAL_IN: 'Manuel giriş',
    MANUAL_OUT: 'Manuel çıkış',
    COLLECTION: 'Tahsilat',
    PAYMENT: 'Ödeme',
    TRANSFER: 'Transfer',
    TRANSFER_IN: 'Transfer giriş',
    TRANSFER_OUT: 'Transfer çıkış',
    REVERSAL: 'Tersine işlem',
    ADJUSTMENT: 'Bakiye düzeltmesi',
    OTHER: 'Diğer hesap hareketi',
    EMPLOYEE_ADVANCE: 'Personel avansı',
    PAYROLL: 'Bordro ödemesi'
  },
  source_type: {
    document: 'Belge',
    finance_payment: 'Tahsilat / ödeme',
    finance_account_movement: 'Manuel hesap hareketi',
    finance_transfer: 'Hesap transferi',
    employee_advance_transaction: 'Personel avansı',
    finance_manual_entry: 'Manuel cari kaydı',
    finance_party_transfer: 'Cari virman',
    INVENTORY_COMMAND: 'Stok işlemi',
    MANUAL_STOCK_MOVEMENT: 'Manuel stok hareketi',
    STOCK_MOVEMENT_REVERSAL: 'Stok hareketi ters kaydı',
    STOCK_COUNT: 'Stok sayımı',
    WAREHOUSE_TRANSFER: 'Depo transferi',
    PURCHASE_INVOICE: 'Alış faturası',
    SALES_INVOICE: 'Satış faturası'
  },
  document_type_code: {
    SALES_QUOTE: 'Satış teklifi',
    SALES_ORDER: 'Satış siparişi',
    SALES_DELIVERY: 'Satış irsaliyesi',
    SALES_INVOICE: 'Satış faturası',
    SALES_RETURN_INVOICE: 'Satış iade faturası',
    PURCHASE_QUOTE: 'Alış teklifi',
    PURCHASE_ORDER: 'Alış siparişi',
    PURCHASE_DELIVERY: 'Alış irsaliyesi',
    PURCHASE_INVOICE: 'Alış faturası',
    PURCHASE_RETURN_INVOICE: 'Alış iade faturası'
  },
  source_document_type: {
    SALES_QUOTE: 'Satış teklifi',
    SALES_ORDER: 'Satış siparişi',
    SALES_DELIVERY: 'Satış irsaliyesi',
    SALES_INVOICE: 'Satış faturası',
    SALES_RETURN_INVOICE: 'Satış iade faturası',
    PURCHASE_ORDER: 'Alış siparişi',
    PURCHASE_DELIVERY: 'Alış irsaliyesi',
    PURCHASE_INVOICE: 'Alış faturası',
    PURCHASE_RETURN_INVOICE: 'Alış iade faturası'
  },
  payment_method: {
    CASH: 'Kasa',
    BANK: 'Banka',
    POS: 'POS',
    CHECK: 'Çek',
    PROMISSORY_NOTE: 'Senet',
    OTHER: 'Diğer'
  },
  account_type: {
    CASH: 'Kasa',
    BANK: 'Banka',
    POS: 'POS',
    OTHER: 'Diğer'
  },
  warehouse_type: {
    STANDARD: 'Standart',
    QUARANTINE: 'Karantina',
    TRANSIT: 'Transit',
    RETURN: 'İade'
  },
  transfer_type: {
    QUICK: 'Hızlı transfer',
    WORKFLOW: 'Onaylı transfer'
  },
  count_type: {
    FULL: 'Tam sayım',
    PARTIAL: 'Kısmi sayım'
  },
  work_type: {
    FULL_TIME: 'Tam zamanlı',
    PART_TIME: 'Yarı zamanlı',
    INTERN: 'Stajyer',
    CONTRACT: 'Sözleşmeli'
  },
  wage_type: {
    GROSS: 'Brüt',
    NET: 'Net'
  },
  wage_period: {
    MONTHLY: 'Aylık',
    DAILY: 'Günlük',
    HOURLY: 'Saatlik'
  },
  contribution_scheme_code: {
    NO_DISCOUNT: 'İndirimsiz',
    OTHER_SECTOR: 'Diğer sektör (2 puan)',
    MANUFACTURING: 'İmalat (5 puan)'
  },
  prior_employer_tax_policy: {
    SEPARATE: 'Ayrı hesap',
    CARRY: 'Devir'
  },
  sensitivity: {
    GENERAL: 'Genel',
    IDENTITY: 'Kimlik',
    HEALTH: 'Sağlık'
  },
  payroll_treatment: {
    PAID: 'Ücretli',
    UNPAID: 'Ücretsiz',
    SICK_REQUIRES_REVIEW: 'Raporlu (ücretli)'
  },
  employee_status: {
    ACTIVE: 'Aktif',
    INACTIVE: 'Pasif',
    ARCHIVED: 'Arşivlendi'
  },
  timesheet_source: {
    GENERATED: 'Otomatik',
    MANUAL: 'Elle'
  },
  run_type: {
    REGULAR: 'Normal',
    OFF_CYCLE: 'Ek bordro',
    CORRECTION: 'Düzeltme'
  },
  event: {
    REQUESTED: 'Talep edildi',
    APPROVED: 'Onaylandı',
    IN_TRANSIT: 'Yolda',
    PARTIALLY_RECEIVED: 'Kısmi teslim',
    RECEIVED: 'Teslim alındı',
    COMPLETED: 'Tamamlandı',
    CANCELLED: 'İptal edildi',
    DELIVER: 'Teslim',
    RETURN: 'İade',
    WASTE: 'Fire / Zayi'
  },
  status: {
    DRAFT: 'Taslak',
    ACTIVE: 'Aktif',
    INACTIVE: 'Pasif',
    APPROVED: 'Onaylandı',
    POSTED: 'İşlendi',
    ISSUED: 'Kesildi',
    CANCELLED: 'İptal',
    REVERSED: 'Ters kayıt',
    REQUESTED: 'Talep edildi',
    IN_TRANSIT: 'Sevk sırasında',
    PARTIALLY_RECEIVED: 'Kısmi teslim',
    RECEIVED: 'Teslim alındı',
    COMPLETED: 'Tamamlandı',
    SENT: 'Gönderildi',
    ACCEPTED: 'Kabul edildi',
    REJECTED: 'Reddedildi',
    EXPIRED: 'Süresi doldu',
    CONFIRMED: 'Onaylandı',
    PARTIALLY_FULFILLED: 'Kısmi karşılandı',
    FULFILLED: 'Tamamlandı'
  },
  state: {
    DRAFT: 'Taslak',
    ACTIVE: 'Aktif',
    INACTIVE: 'Pasif',
    APPROVED: 'Onaylandı',
    POSTED: 'İşlendi',
    ISSUED: 'Kesildi',
    CANCELLED: 'İptal',
    REVERSED: 'Ters kayıt',
    REQUESTED: 'Talep edildi',
    IN_TRANSIT: 'Sevk sırasında',
    PARTIALLY_RECEIVED: 'Kısmi teslim',
    RECEIVED: 'Teslim alındı',
    COMPLETED: 'Tamamlandı',
    SENT: 'Gönderildi',
    ACCEPTED: 'Kabul edildi',
    REJECTED: 'Reddedildi',
    EXPIRED: 'Süresi doldu',
    CONFIRMED: 'Onaylandı',
    PARTIALLY_FULFILLED: 'Kısmi karşılandı',
    FULFILLED: 'Tamamlandı'
  },
  document_status: {
    DRAFT: 'Taslak',
    ACTIVE: 'Aktif',
    INACTIVE: 'Pasif',
    APPROVED: 'Onaylandı',
    POSTED: 'İşlendi',
    ISSUED: 'Kesildi',
    CANCELLED: 'İptal',
    REVERSED: 'Ters kayıt',
    SENT: 'Gönderildi',
    ACCEPTED: 'Kabul edildi',
    REJECTED: 'Reddedildi',
    EXPIRED: 'Süresi doldu',
    CONFIRMED: 'Onaylandı',
    PARTIALLY_FULFILLED: 'Kısmi karşılandı',
    FULFILLED: 'Tamamlandı'
  },
  permission: {
    'organization.company.edit': 'Şirket bilgilerini düzenleme',
    'party.read': 'Cari kart görüntüleme',
    'party.edit': 'Cari kart düzenleme',
    'product.read': 'Stok kartı görüntüleme',
    'product.edit': 'Stok kartı düzenleme',
    'product.reference.manage': 'Stok tanımı yönetimi',
    'product.variant.manage': 'Varyant yönetimi',
    'product.variant_definition.manage': 'Varyant tanımı yönetimi',
    'inventory.movement.post': 'Stok hareketi işleme',
    'inventory.transfer.request': 'Depo transferi oluşturma',
    'inventory.transfer.receive': 'Depo transferi teslim alma',
    'pricing.read': 'Fiyat listesi görüntüleme',
    'pricing.manage': 'Fiyat listesi yönetimi',
    'tax.read': 'Vergi tanımı görüntüleme',
    'tax.manage': 'Vergi tanımı yönetimi',
    'security.role.manage': 'Rol yönetimi',
    'security.user.manage': 'Kullanıcı yönetimi',
    'security.token.manage': 'Güvenlik anahtarı yönetimi',
    'purchase.order.read': 'Alış siparişi görüntüleme',
    'purchase.order.manage': 'Alış siparişi yönetimi',
    'purchase.receipt.post': 'Alış mal kabulü kaydetme',
    'purchase.invoice.post': 'Alış faturası kaydetme',
    'purchase.invoice.standalone': 'Bağımsız alış faturası kaydetme',
    'purchase.return.post': 'Alış iadesi kaydetme',
    'purchase.reference.manage': 'Tedarikçi ürün eşleştirme'
  }
};

function findLabel(labels: Record<string, string>, value: string) {
  return labels[value] ?? labels[value.toUpperCase()] ?? labels[value.toLowerCase()];
}

export function localizedEnum(value: unknown, fieldKey: string | string[]) {
  const text = String(value ?? '').trim();
  if (!text) return text;
  const keys = Array.isArray(fieldKey) ? fieldKey : [fieldKey];
  for (const key of keys) {
    const normalizedKey = key.split('.').at(-1) ?? key;
    const label = findLabel(enumLabels[normalizedKey] ?? {}, text);
    if (label) return label;
  }
  const financeSafeFields = new Set([
    'status',
    'state',
    'document_status',
    'movement_kind',
    'payment_method',
    'account_type',
    'direction',
    'payment_kind'
  ]);
  if (keys.some((key) => financeSafeFields.has(key.split('.').at(-1) ?? key))) {
    return 'Bilinmeyen durum';
  }
  return text;
}

export function localizedStatus(value: unknown) {
  return localizedEnum(value, ['status', 'state', 'document_status']);
}

export function localizedPermission(value: unknown) {
  return localizedEnum(value, 'permission');
}

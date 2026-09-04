<script lang="ts">
  import { page } from '$app/state';
  import { beforeNavigate, goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { toast } from 'svelte-sonner';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import { ReasonDialog } from '$lib/components/varya/reason-dialog';
  import {
    ArrowLeft,
    Ban,
    Check,
    CopyPlus,
    Plus,
    Printer,
    Save,
    Trash2,
    WalletCards,
    X
  } from '@lucide/svelte';
  import { api, APIRequestError, type Company, type Session } from '$lib/api';
  import * as Field from '$lib/components/ui/field';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { DocumentToolbar } from '$lib/components/varya/document-toolbar';
  import { EntityPickerDialog } from '$lib/components/varya/entity-picker-dialog';
  import { EntityCombobox } from '$lib/components/varya/entity-combobox';
  import { MoneyInput } from '$lib/components/varya/money-input';
  import { QuantityInput } from '$lib/components/varya/quantity-input';
  import { DateInput } from '$lib/components/varya/date-input';
  import { listenVaryaShortcut } from '$lib/components/varya/keyboard';
  import { StateBlock } from '$lib/components/varya/status';
  import {
    isActiveStandardWarehouse,
    warehouseOptionLabel,
    type Warehouse
  } from '$lib/features/warehouses/types';
  import { getParty, listParties } from '$lib/features/parties/api';
  import type { Party } from '$lib/features/parties/types';
  import { printDocument } from '$lib/design/print';
  import { printableCompany } from '$lib/features/settings/company-profile';
  import { buildCommercialDocument } from './commercial-print';
  import { getExchangeRateDashboard, listPricingCurrencies } from '$lib/features/pricing/api';
  import type { ExchangeRateDashboard, PricingCurrency } from '$lib/features/pricing/types';
  import { canonicalDecimal, trimDecimalZeros } from '$lib/design/decimal';
  import {
    decimalParts,
    decimalSubtract,
    decimalDivide,
    lineAmounts,
    formTotals as computeFormTotals,
    lineComponentAmounts
  } from './commercial-calc';
  import { buildDocumentPayload } from './commercial-payload';
  import {
    text,
    dateOnly,
    optionFromID,
    emptyForm,
    emptyLine as makeEmptyLine,
    sourceDocumentNumber,
    contextualDocumentStatusLabel as contextualStatusLabel,
    lineFromRecord as mapLineFromRecord,
    sourceOptionFromReference as mapSourceOption,
    sourceReferences as mapSourceReferences,
    sourceReferenceLabel
  } from './commercial-record';
  import {
    formatDate,
    formatMoney,
    formatQuantity,
    formatQuantityWithUnit
  } from '$lib/design/formatters';
  import {
    commercialPath,
    commercialConfig,
    commercialDocumentHref,
    commercialResource,
    commercialResourceFromReference,
    commercialStatusLabels,
    documentStatusTone,
    type CommercialDirection,
    type CommercialDocumentReference,
    type CommercialResource,
    type CommercialLineType
  } from './types';
  import type {
    ProductOption,
    VariantOption,
    WarehouseOption,
    PartyOption,
    SourceOption,
    ServerErrorDetail,
    ConversionAction,
    DocumentLine,
    DocumentRecord,
    LineDraft,
    LineTaxComponent,
    DocumentForm
  } from './editor-types';

  let {
    direction,
    resource,
    mode = 'detail'
  }: {
    direction: CommercialDirection;
    resource: CommercialResource | undefined;
    mode?: 'new' | 'detail';
  } = $props();

  let session = $state<Session | null>(null);
  let company = $state<Company>();
  let partyDetail = $state<Party>();
  let record = $state<DocumentRecord>();
  let form = $state<DocumentForm>(emptyForm());
  let lines = $state<LineDraft[]>([]);
  let warehouses = $state<WarehouseOption[]>([]);
  let currencyOptions = $state<PricingCurrency[]>([]);
  let baseCurrency = $state('TRY');
  let exchangeRates = $state<Record<string, string>>({ TRY: '1' });
  let variantRequestSequence = 0;
  let loading = $state(true);
  let saving = $state(false);
  let submitting = $state(false);
  let denied = $state(false);
  let error = $state('');
  let validationError = $state('');
  let deleteConfirmOpen = $state(false);
  let cancelDialogOpen = $state(false);
  let genericConfirmOpen = $state(false);
  let genericConfirm = $state<{
    title: string;
    description: string;
    confirmLabel: string;
    run: () => void | Promise<void>;
  } | null>(null);
  function askConfirm(config: {
    title: string;
    description: string;
    confirmLabel: string;
    run: () => void | Promise<void>;
  }) {
    genericConfirm = config;
    genericConfirmOpen = true;
  }
  let referencesLoading = $state(false);
  let referencesError = $state('');
  let sourceLoading = $state(false);
  let sourceError = $state('');
  let sourcePickerKey = $state(0);
  let validationElement = $state<HTMLDivElement>();
  let serverErrors = $state<ServerErrorDetail[]>([]);
  let staleConflict = $state(false);
  let dirtyTrackingReady = $state(false);
  let cleanSnapshot = $state('');
  let navigationInProgress = $state(false);
  const commandKeys = new Map<string, string>();

  const config = $derived(commercialConfig(direction, resource));
  const routePath = $derived(commercialPath(direction, resource));
  const isSales = $derived(direction === 'sales');
  const isNew = $derived(mode === 'new');
  const endpoint = $derived(config?.endpoint ?? '');
  const isPurchaseOrder = $derived(!isSales && resource === 'orders');
  const canManage = $derived(
    Boolean(
      session &&
      config &&
      (session.permissions.includes(config.managePermission) ||
        session.permissions.includes('commercial.document.manage'))
    )
  );
  // Preparing/editing a draft is gated by the ".draft" permission (a poster can
  // also prepare); finalizing/cancelling still needs canManage / the server's
  // available_actions.
  const canDraft = $derived(
    Boolean(
      session &&
      config &&
      (canManage ||
        session.permissions.includes(config.draftPermission) ||
        session.permissions.includes('commercial.document.manage'))
    )
  );
  const isEditable = $derived(Boolean(isNew ? canDraft : record?.status === 'DRAFT' && canDraft));
  const editorMutationDisabled = $derived(!isEditable || sourceLoading || saving || submitting);
  const currentSnapshot = $derived.by(() => formSnapshot());
  const isDirty = $derived(
    dirtyTrackingReady && !navigationInProgress && cleanSnapshot !== currentSnapshot
  );
  const documentStatus = $derived(record?.status ?? (isNew ? 'DRAFT' : ''));
  const canUseService = $derived(isSales || resource === 'orders' || resource === 'invoices');
  const requiresHeaderWarehouse = $derived(!isSales && resource !== 'invoices');
  const headerNumber = $derived(
    text(
      record?.document_no ??
        record?.order_no ??
        record?.receipt_no ??
        record?.invoice_no ??
        record?.return_no
    )
  );
  const currency = $derived(form.currency || 'TRY');
  const financeSettlementAllowsAction = $derived.by(() => {
    const settlement = record?.settlement;
    if (!settlement || text(settlement.return_status).toUpperCase() === 'FULL') return false;
    const remaining = isSales ? settlement.amount_due : settlement.amount_payable;
    return decimalParts(text(remaining, '0'))[0] > 0n;
  });
  const canOpenFinanceAction = $derived(
    Boolean(
      !isNew &&
      resource === 'invoices' &&
      documentStatus === 'POSTED' &&
      record?.id &&
      (record.party_id || record.supplier_id) &&
      session?.permissions.includes('finance.payment.post') &&
      record.available_actions?.[isSales ? 'can_collect' : 'can_pay'] === true &&
      financeSettlementAllowsAction
    )
  );

  function serverActionAllowed(
    action: keyof NonNullable<DocumentRecord['available_actions']>,
    fallback = true
  ) {
    return record?.available_actions?.[action] ?? fallback;
  }

  function financeActionPath() {
    if (!record?.id) return '';
    const path = isSales ? '/cari/tahsilatlar' : '/cari/odemeler';
    const params = new URLSearchParams({
      auto_open: 'true',
      party_id: text(record.party_id ?? record.supplier_id),
      currency: currency,
      document_id: record.id
    });
    return `${path}?${params}`;
  }

  function isLineTypeAllowed(lineType: CommercialLineType) {
    return lineType === 'PRODUCT' || canUseService;
  }

  function formSnapshot() {
    return JSON.stringify({
      documentNo: form.documentNo,
      partyID: form.party?.id ?? '',
      branchID: form.branchID,
      defaultWarehouseID: form.defaultWarehouse?.id ?? '',
      documentDate: form.documentDate,
      dueDate: form.dueDate,
      validUntil: form.validUntil,
      currency: form.currency,
      exchangeRate: form.exchangeRate,
      notes: form.notes,
      sourceDocumentIDs: form.sourceDocuments.map((source) => source.id),
      sourceKind: form.sourceKind,
      reason: form.reason,
      lines: lines.map((line) => ({
        id: line.id ?? '',
        lineType: line.lineType,
        productID: line.product?.id ?? '',
        variantID: line.variant?.id ?? '',
        warehouseID: line.warehouse?.id ?? '',
        unitCode: line.unitCode,
        quantity: line.quantity,
        conversionFactor: line.conversionFactor,
        unitPrice: line.unitPrice,
        discountRate: line.discountRate,
        taxRate: line.taxRate,
        taxIncluded: line.taxIncluded,
        taxComponents: line.taxComponents,
        description: line.description,
        sourceLineID: line.sourceLineID ?? '',
        purchaseOrderLineID: line.purchaseOrderLineID ?? '',
        goodsReceiptLineID: line.goodsReceiptLineID ?? ''
      }))
    });
  }

  function markClean() {
    cleanSnapshot = formSnapshot();
    dirtyTrackingReady = true;
  }

  const sourceRefContext = $derived({ direction, isSales });
  function sourceOptionFromReference(
    value: unknown,
    fallbackResource?: string,
    fallbackDocumentNo = ''
  ): SourceOption | null {
    return mapSourceOption(value, sourceRefContext, fallbackResource, fallbackDocumentNo);
  }
  function sourceReferences(next: DocumentRecord): SourceOption[] {
    return mapSourceReferences(next, sourceRefContext);
  }

  function shouldPromptForUnsavedChanges() {
    return Boolean(isEditable && isDirty && !navigationInProgress);
  }

  let unsavedNavOpen = $state(false);
  let pendingNavUrl = $state<string | null>(null);

  beforeNavigate((navigation) => {
    if (!shouldPromptForUnsavedChanges()) return;
    // Tarayıcıdan tamamen ayrılırken tarayıcının kendi uyarısına bırak.
    if (navigation.type === 'leave') {
      navigation.cancel();
      return;
    }
    navigation.cancel();
    pendingNavUrl = navigation.to?.url.href ?? null;
    unsavedNavOpen = true;
  });

  async function confirmLeaveUnsaved() {
    const target = pendingNavUrl;
    unsavedNavOpen = false;
    pendingNavUrl = null;
    navigationInProgress = true;
    if (target) await goto(target);
  }

  function emptyLine(lineType: CommercialLineType = 'PRODUCT'): LineDraft {
    return makeEmptyLine(lineType, form.defaultWarehouse);
  }

  function warehouseOption(id: unknown, name?: string, code?: string): WarehouseOption | null {
    const value = text(id);
    if (!value) return null;
    const known = warehouses.find((item) => item.id === value);
    if (known) return known;
    const resolvedName = text(name);
    if (resolvedName) {
      const prefix = text(code) ? `${text(code)} · ` : '';
      return { id: value, title: `${prefix}${resolvedName}` };
    }
    return { id: value, title: 'Seçili depo' };
  }

  function lineFromRecord(line: DocumentLine): LineDraft {
    return mapLineFromRecord(line, warehouseOption);
  }

  function hydrate(next: DocumentRecord) {
    record = next;
    const partyID = isSales ? next.party_id : next.supplier_id;
    const partyTitle = text(next.party_name ?? next.supplier_name, 'Seçili cari');
    const sourceDocuments = sourceReferences(next);
    const sourceDocument = sourceDocuments[0] ?? null;
    const sourceID = sourceDocument?.id ?? '';
    const inferredSourceResource = isSales
      ? ((
          {
            QUOTE: 'quotes',
            ORDER: 'orders',
            DISPATCH: 'dispatches',
            RECEIPT: 'dispatches',
            INVOICE: 'invoices'
          } as Record<string, string>
        )[text(next.source_kind).toUpperCase()] ?? sourceDocument?.kind)
      : next.goods_receipt_id
        ? 'dispatches'
        : next.purchase_order_id
          ? 'orders'
          : (sourceDocument?.kind ?? '');
    form = {
      documentNo: text(
        next.document_no ?? next.order_no ?? next.receipt_no ?? next.invoice_no ?? next.return_no
      ),
      party: optionFromID(partyID, partyTitle) as PartyOption | null,
      branchID: text(next.branch_id),
      branchName: text(next.branch_name, 'Seçili şube'),
      defaultWarehouse: warehouseOption(
        next.default_warehouse_id ?? next.warehouse_id,
        next.default_warehouse_name ?? next.warehouse_name,
        next.default_warehouse_code ?? next.warehouse_code
      ),
      documentDate: dateOnly(
        next.document_date ??
          next.order_date ??
          next.receipt_date ??
          next.invoice_date ??
          next.return_date
      ),
      dueDate: dateOnly(next.due_date, ''),
      validUntil: dateOnly(next.valid_until, ''),
      currency: text(next.currency_code ?? next.currency, 'TRY'),
      exchangeRate: trimDecimalZeros(text(next.exchange_rate, '1')) || '1',
      notes: text(next.notes),
      sourceDocumentID: sourceID,
      sourceDocument,
      sourceDocuments,
      sourceKind: text(
        next.source_kind,
        sourceID ? sourceKindForResource(inferredSourceResource) : 'DIRECT'
      ),
      reason: text(next.reason)
    };
    lines = (next.lines ?? []).map(lineFromRecord);
    serverErrors = [];
    sourceError = '';
    staleConflict = false;
    sourcePickerKey += 1;
    markClean();
    void loadVariantOptions(lines);
  }

  async function loadReferences() {
    referencesLoading = true;
    referencesError = '';
    try {
      const branchQuery = form.branchID ? `&branch_id=${encodeURIComponent(form.branchID)}` : '';
      const result = await api<{ items?: Warehouse[] }>(`/warehouses?limit=100${branchQuery}`);
      warehouses = (result.items ?? []).filter(isActiveStandardWarehouse).map((warehouse) => ({
        id: warehouse.id,
        title: warehouseOptionLabel(warehouse),
        branchID: warehouse.branch_id ?? undefined
      }));
      if (isNew && warehouses.length > 0) {
        const first = warehouses[0];
        form.defaultWarehouse = first;
        if (!form.branchID && first.branchID) {
          form.branchID = first.branchID;
          form.branchName = first.subtitle ?? 'Şube';
        }
      } else {
        reconcileWarehouseLabels();
      }
    } catch (cause) {
      referencesError = friendlyError(cause, 'Depolar yüklenemedi.');
    } finally {
      referencesLoading = false;
    }
  }

  // An existing document is hydrated before this reference list finishes
  // loading, so its warehouse options are built from an empty list and keep
  // the "Seçili depo" placeholder title. Re-resolve the real labels once the
  // warehouses are in.
  function reconcileWarehouseLabels() {
    const byID = new Map(warehouses.map((item) => [item.id, item]));
    const relabel = (option: WarehouseOption | null): WarehouseOption | null => {
      if (!option) return option;
      const match = byID.get(option.id);
      return match
        ? { ...option, title: match.title, branchID: match.branchID ?? option.branchID }
        : option;
    };
    form.defaultWarehouse = relabel(form.defaultWarehouse);
    for (const line of lines) line.warehouse = relabel(line.warehouse);
  }

  async function loadCurrencyReferences() {
    try {
      const [currencyResult, rateResult] = await Promise.all([
        listPricingCurrencies(false),
        getExchangeRateDashboard()
      ]);
      currencyOptions = currencyResult.items ?? [];
      const resolvedBaseCurrency = rateResult.base_currency || baseCurrency;
      baseCurrency = resolvedBaseCurrency;
      const nextRates: Record<string, string> = { [resolvedBaseCurrency]: '1' };
      for (const item of rateResult.items ?? [])
        nextRates[item.currency_code] = trimDecimalZeros(item.rate_to_base) || '1';
      exchangeRates = nextRates;
      if (nextRates[currency]) form.exchangeRate = nextRates[currency];
      for (const line of lines) {
        if (!line.manualPrice && line.baseUnitPrice)
          line.unitPrice = priceFromBase(line.baseUnitPrice);
      }
    } catch {
      // Sales and purchasing remain usable for base-currency documents when
      // the settings permission or a provider is unavailable. The server is
      // still authoritative and fails closed for a missing foreign rate.
    }
  }

  async function changeCurrency(nextCurrency: string) {
    const next = nextCurrency.toUpperCase();
    if (next !== baseCurrency && !exchangeRates[next]) {
      try {
        const result = await getExchangeRateDashboard();
        const resolvedBaseCurrency = result.base_currency || baseCurrency;
        baseCurrency = resolvedBaseCurrency;
        const nextRates: Record<string, string> = { [resolvedBaseCurrency]: '1' };
        for (const item of result.items ?? [])
          nextRates[item.currency_code] = trimDecimalZeros(item.rate_to_base) || '1';
        exchangeRates = nextRates;
      } catch (cause) {
        validationError = friendlyError(cause, 'Seçilen para birimi için güncel kur alınamadı.');
        return;
      }
    }
    form.currency = next;
    form.exchangeRate = exchangeRates[next] || '1';
    for (const line of lines) {
      if (!line.manualPrice && line.baseUnitPrice)
        line.unitPrice = priceFromBase(line.baseUnitPrice);
    }
  }

  async function load() {
    if (!config || !page.params.id) return;
    loading = true;
    error = '';
    try {
      const next = await api<DocumentRecord>(`${endpoint}/${encodeURIComponent(page.params.id)}`);
      hydrate(next);
    } catch (cause) {
      error = friendlyError(cause, 'Belge bilgileri alınamadı.');
    } finally {
      loading = false;
    }
  }

  async function searchParties(query: string, signal: AbortSignal): Promise<PartyOption[]> {
    const params = new URLSearchParams({ limit: '30', role: isSales ? 'customer' : 'supplier' });
    if (query.trim()) params.set('q', query.trim());
    const result = await listParties(params, signal);
    return result.items.map((party) => ({
      id: party.id,
      title: party.trade_name || party.display_name || party.legal_name || party.code,
      subtitle: party.code,
      code: party.code,
      meta: [party.is_customer ? 'Müşteri' : '', party.is_supplier ? 'Tedarikçi' : ''].filter(
        Boolean
      )
    }));
  }

  async function searchProducts(
    lineType: CommercialLineType,
    query: string,
    signal: AbortSignal
  ): Promise<ProductOption[]> {
    const params = new URLSearchParams({
      limit: '30',
      kind: lineType === 'SERVICE' ? 'SERVICE' : 'PHYSICAL'
    });
    if (query.trim()) params.set('q', query.trim());
    const result = await api<{ items?: Array<Record<string, unknown>> }>(`/products?${params}`, {
      signal
    });
    return (result.items ?? []).map((product) => {
      const baseUnitPrice = trimDecimalZeros(
        text(product[isSales ? 'sales_price' : 'purchase_price'])
      );
      return {
        id: text(product.id),
        title: text(product.name, 'Stok kartı'),
        subtitle: text(product.code ?? product.sku),
        code: text(product.code ?? product.sku),
        kind: text(product.kind).toUpperCase() === 'SERVICE' ? 'SERVICE' : 'PHYSICAL',
        unit: text(product.stock_unit ?? product.base_unit, 'ADET'),
        baseUnitPrice,
        unitPrice: priceFromBase(baseUnitPrice),
        taxRate:
          trimDecimalZeros(text(product[isSales ? 'sales_tax_rate' : 'purchase_tax_rate'], '0')) ||
          '0',
        taxIncluded:
          product[isSales ? 'sales_tax_included' : 'purchase_tax_included'] === true ||
          String(
            product[isSales ? 'sales_tax_included' : 'purchase_tax_included']
          ).toLowerCase() === 'true',
        taxComponents: productTaxComponents(
          product[isSales ? 'sales_tax_components' : 'purchase_tax_components']
        ),
        variantsEnabled: product.variants_enabled === true,
        meta: text(product.kind).toUpperCase() === 'SERVICE' ? 'Hizmet' : 'Ürün'
      };
    });
  }

  /** The card's taxes besides KDV, as the catalog resolved them. */
  function productTaxComponents(value: unknown): LineTaxComponent[] {
    if (!Array.isArray(value)) return [];
    const components: LineTaxComponent[] = [];
    for (const entry of value) {
      if (!entry || typeof entry !== 'object') continue;
      const record = entry as Record<string, unknown>;
      const code = text(record.code);
      const name = text(record.name) || code;
      if (!code && !name) continue;
      const calculationType = text(record.calculation_type).toUpperCase();
      components.push({
        code,
        name,
        calculationType:
          calculationType === 'QUANTITY_BASED' || calculationType === 'FIXED_AMOUNT'
            ? calculationType
            : 'PERCENTAGE',
        rate: trimDecimalZeros(text(record.rate, '0')) || '0',
        includedInBase: record.included_in_base === true
      });
    }
    return components;
  }

  function priceFromBase(basePrice: string) {
    if (!basePrice) return '';
    if (currency === baseCurrency) return basePrice;
    const rate = exchangeRates[currency];
    return rate ? decimalDivide(basePrice, rate) : '';
  }

  // A service card often only carries a price in one direction (e.g. a sales
  // price but no purchase price). When the resolved unit price is zero for a
  // service, leave the field blank so the user notices they must enter it,
  // rather than silently invoicing at 0.
  function resolveLineUnitPrice(
    lineType: CommercialLineType,
    optionPrice: string | undefined,
    basePrice: string
  ) {
    const resolved = text(optionPrice) || priceFromBase(basePrice);
    if (lineType === 'SERVICE' && (!resolved || Number(resolved) === 0)) return '';
    return resolved;
  }

  function variantTitle(item: Record<string, unknown>) {
    const values = Array.isArray(item.values) ? item.values : [];
    const labels = values
      .filter((value): value is Record<string, unknown> =>
        Boolean(value && typeof value === 'object')
      )
      .map((value) => text(value.option_name ?? value.option_code ?? value.definition_name))
      .filter(Boolean);
    return labels.join(' / ') || text(item.variant_code, 'Varyant');
  }

  function mapVariant(item: Record<string, unknown>, product: ProductOption): VariantOption {
    const override = text(item[isSales ? 'sales_price_override' : 'purchase_price_override']);
    const inherited = text(item[isSales ? 'sales_price' : 'purchase_price']);
    const baseUnitPrice = trimDecimalZeros(override || inherited || product.baseUnitPrice);
    return {
      id: text(item.id),
      title: variantTitle(item),
      subtitle: text(item.variant_code),
      variantCode: text(item.variant_code),
      unit: text(item.stock_unit, product.unit || 'ADET'),
      baseUnitPrice,
      meta: text(item.variant_code)
    };
  }

  async function loadVariantOptions(targetLines: LineDraft[]) {
    await Promise.all(targetLines.map((line) => loadVariantOptionsForLine(line)));
  }

  function anyVariantLoading() {
    return lines.some((l) => l.variantLoading);
  }

  async function loadVariantOptionsForLine(line: LineDraft) {
    if (line.lineType !== 'PRODUCT' || !line.product?.id) return;
    const requestKey = ++variantRequestSequence;
    line.variantRequestKey = requestKey;
    line.variantLoading = true;
    line.variantError = '';
    const expectedVariants = Boolean(line.product.variantsEnabled || line.variant);
    try {
      const result = await api<{ items?: Array<Record<string, unknown>> }>(
        `/products/${encodeURIComponent(line.product.id)}/variants`
      );
      if (line.variantRequestKey !== requestKey) return;
      line.variants = (result.items ?? [])
        .filter((item) => item.is_active !== false && text(item.id))
        .map((item) => mapVariant(item, line.product!));
      line.product = { ...line.product, variantsEnabled: line.variants.length > 0 };
      const selected = line.variants.find((item) => item.id === line.variant?.id);
      if (selected) {
        // Existing documents carry their own variant snapshot. Do not let a
        // later master-data edit silently rewrite the visible history or the
        // persisted price/unit while hydrating the editor.
        if (!line.variantSnapshot) {
          line.variant = selected;
          line.unitCode = selected.unit || line.unitCode;
          line.baseUnitPrice = selected.baseUnitPrice || '';
          line.unitPrice = priceFromBase(selected.baseUnitPrice || '');
        }
      } else if (line.variant) {
        line.variantError = 'Seçili varyant artık aktif değil.';
      }
      if (line.variants.length === 0 && expectedVariants) {
        line.variantError = 'Bu stok kartında aktif varyant bulunamadı.';
      }
    } catch (cause) {
      if (line.variantRequestKey === requestKey)
        line.variantError = friendlyError(cause, 'Varyant bilgisi alınamadı.');
    } finally {
      // Always clear the loading flag, even for a superseded request: a
      // stale response must not overwrite variant data, but leaving the
      // flag stuck true forever (blocking Save) is worse than a brief
      // inaccuracy while a newer request is still in flight.
      line.variantLoading = false;
      if (validationError.endsWith('varyantları yükleniyor.')) validationError = '';
    }
  }

  function sourceResources() {
    if (isSales) {
      if (resource === 'orders') return ['quotes'];
      if (resource === 'dispatches') return ['orders'];
      if (resource === 'invoices') return ['dispatches', 'orders'];
      if (resource === 'returns') return ['invoices', 'dispatches'];
    } else {
      if (resource === 'dispatches') return ['orders'];
      if (resource === 'invoices') return ['orders', 'dispatches'];
      if (resource === 'returns') return ['dispatches'];
    }
    return [];
  }

  function sourceInitialEmptyText() {
    if (isSales && resource === 'orders') {
      return 'Siparişe aktarılabilecek kabul edilmiş teklif bulunamadı. Önce teklifi kabul edin.';
    }
    return 'Aramak için bir kelime yazın veya listeden seçim yapın.';
  }

  function sourceEmptyText() {
    if (isSales && resource === 'orders') {
      return 'Aramanızla eşleşen kabul edilmiş teklif bulunamadı.';
    }
    return 'Eşleşen kaynak belge bulunamadı.';
  }

  function sourceStatusAllowed(sourceResource: string, status: unknown) {
    const value = text(status).toUpperCase();
    if (isSales) {
      if (resource === 'orders' && sourceResource === 'quotes') return value === 'ACCEPTED';
      if (resource === 'dispatches' && sourceResource === 'orders') {
        return value === 'OPEN' || value === 'CONFIRMED' || value === 'PARTIALLY_FULFILLED';
      }
      if (resource === 'invoices' && sourceResource === 'dispatches')
        return value === 'FINALIZED' || value === 'POSTED';
      if (resource === 'invoices' && sourceResource === 'orders') {
        return (
          value === 'OPEN' ||
          value === 'CONFIRMED' ||
          value === 'PARTIALLY_FULFILLED' ||
          value === 'FULFILLED'
        );
      }
      if (resource === 'returns') return value === 'FINALIZED' || value === 'POSTED';
      return false;
    }
    if (resource === 'dispatches' && sourceResource === 'orders') {
      return value === 'OPEN' || value === 'CONFIRMED' || value === 'PARTIALLY_FULFILLED';
    }
    if (resource === 'invoices' && sourceResource === 'orders') {
      return (
        value === 'OPEN' ||
        value === 'CONFIRMED' ||
        value === 'PARTIALLY_FULFILLED' ||
        value === 'FULFILLED'
      );
    }
    if (resource === 'invoices' && sourceResource === 'dispatches')
      return value === 'FINALIZED' || value === 'POSTED';
    if (resource === 'returns' && sourceResource === 'dispatches')
      return value === 'FINALIZED' || value === 'POSTED';
    return false;
  }

  function contextualDocumentStatusLabel(status: unknown) {
    return contextualStatusLabel(status, isSales);
  }

  function axisLabel(axis: keyof typeof commercialStatusLabels, status: unknown) {
    const value = text(status).toUpperCase();
    return commercialStatusLabels[axis]?.[value] ?? value;
  }

  async function searchSourceDocuments(
    query: string,
    signal: AbortSignal
  ): Promise<SourceOption[]> {
    const responses = await Promise.all(
      sourceResources().map(async (sourceResource) => {
        const params = new URLSearchParams({
          limit: '30',
          for_reference: 'true',
          reference_target: resource ?? ''
        });
        if (form.party?.id) params.set(isSales ? 'party_id' : 'supplier_id', form.party.id);
        if (form.branchID) params.set('branch_id', form.branchID);
        if (form.currency) params.set('currency_code', form.currency);
        if (query.trim()) params.set('q', query.trim());
        const path = `${isSales ? '/sales' : '/purchases'}/${sourceResource}`;
        const result = await api<{ items?: Array<Record<string, unknown>> }>(`${path}?${params}`, {
          signal
        });
        return (result.items ?? [])
          .filter(
            (item) =>
              sourceStatusAllowed(sourceResource, item.lifecycle_status ?? item.status) &&
              (sourceResource !== 'orders' ||
                resource === 'invoices' ||
                item.fulfillment_status !== 'FULFILLED')
          )
          .map((item) => {
            const documentNo = sourceDocumentNumber(item);
            return {
              id: text(item.id),
              title: documentNo || 'Belge',
              documentNo,
              subtitle: text(item.party_name ?? item.supplier_name),
              kind: sourceResource,
              meta: contextualDocumentStatusLabel(item.lifecycle_status ?? item.status)
            };
          });
      })
    );
    return responses.flat();
  }

  function clearSource() {
    form.sourceDocument = null;
    form.sourceDocuments = [];
    form.sourceDocumentID = '';
    form.sourceKind = 'DIRECT';
    lines = [];
    sourceError = '';
    sourcePickerKey += 1;
  }

  async function selectSourceAndLoad(option: SourceOption, force = false) {
    if (sourceLoading) return;
    if (!force && lines.length && form.sourceDocumentID && form.sourceDocumentID !== option.id) {
      askConfirm({
        title: 'Kaynağı değiştir',
        description: 'Yeni kaynak seçildiğinde mevcut satırlar değişecektir. Devam edilsin mi?',
        confirmLabel: 'Devam et',
        run: () => selectSourceAndLoad(option, true)
      });
      return;
    }
    sourceLoading = true;
    sourceError = '';
    try {
      const sourceResource = option.kind ?? '';
      const source = await api<DocumentRecord>(
        `${isSales ? '/sales' : '/purchases'}/${sourceResource}/${encodeURIComponent(option.id)}`
      );
      const sourceLines = source.lines ?? [];
      const nextLines = sourceLines
        .map((sourceLine) => lineFromSourceRecord(sourceLine, sourceResource))
        .filter((line) => isLineTypeAllowed(line.lineType) && decimalParts(line.quantity)[0] > 0n);
      if (!nextLines.length) {
        sourceError = 'Kaynak belgede kullanılabilir satır kalmadı.';
        sourcePickerKey += 1;
        return;
      }

      const documentNo = sourceDocumentNumber(source) || option.documentNo || option.title;
      const nextSource: SourceOption = {
        ...option,
        title: documentNo || 'Seçili kaynak belge',
        documentNo,
        kind: sourceResource,
        resource: commercialResource(sourceResource),
        href: commercialDocumentHref(direction, sourceResource, option.id)
      };
      form = {
        ...form,
        sourceDocumentID: option.id,
        sourceDocument: nextSource,
        sourceDocuments: [nextSource],
        sourceKind: sourceKindForResource(sourceResource),
        branchID: source.branch_id && !form.branchID ? source.branch_id : form.branchID,
        branchName: source.branch_id && !form.branchID ? 'Seçili şube' : form.branchName,
        party:
          source.supplier_id || source.party_id
            ? {
                id: text(source.supplier_id ?? source.party_id),
                title: text(source.supplier_name ?? source.party_name, 'Seçili cari'),
                subtitle: text(source.supplier_code ?? source.party_code)
              }
            : form.party
      };
      lines = nextLines;
      // Source conversion starts with only the persisted variant ID. Resolve
      // the same option snapshot used by the picker before rendering the
      // editor so the trigger never remains at the generic fallback label.
      // Mutate through the reactive `lines` array (not the pre-assignment
      // `nextLines` reference) so state updates made inside the async load
      // are tracked and reflected in bindings like the Save button's
      // disabled state.
      await loadVariantOptions(lines);
      sourceError = '';
      sourcePickerKey += 1;
    } catch (cause) {
      sourceError = friendlyError(cause, 'Kaynak belge ayrıntıları alınamadı.');
      sourcePickerKey += 1;
    } finally {
      sourceLoading = false;
    }
  }

  function lineFromSourceRecord(line: DocumentLine, sourceResource: string): LineDraft {
    const draft = lineFromRecord(line);
    const lineType = draft.lineType;
    const ordered = canonicalDecimal(line.ordered_quantity ?? line.quantity ?? '0') || '0';
    const accepted =
      canonicalDecimal(line.received_quantity ?? line.accepted_quantity ?? '0') || '0';
    const invoiced =
      canonicalDecimal(
        text((line as DocumentLine & { invoiced_quantity?: string }).invoiced_quantity, '0')
      ) || '0';
    let remaining = line.quantity ?? line.ordered_quantity ?? line.accepted_quantity ?? '1';
    if (isSales) {
      const remainingField =
        resource === 'dispatches'
          ? sourceResource === 'orders'
            ? line.remaining_fulfillment_quantity
            : undefined
          : resource === 'invoices'
            ? sourceResource === 'orders' || sourceResource === 'dispatches'
              ? line.remaining_invoicing_quantity
              : undefined
            : resource === 'returns'
              ? sourceResource === 'invoices' || sourceResource === 'dispatches'
                ? line.remaining_return_quantity
                : undefined
              : undefined;
      if (remainingField !== undefined) remaining = remainingField;
    } else if (sourceResource === 'orders') {
      remaining =
        lineType === 'SERVICE'
          ? decimalSubtract(ordered, invoiced)
          : resource === 'invoices'
            ? decimalSubtract(accepted, invoiced)
            : decimalSubtract(ordered, accepted);
    } else if (sourceResource === 'dispatches') {
      if (resource === 'invoices') {
        remaining =
          line.remaining_invoicing_quantity ?? line.accepted_quantity ?? line.quantity ?? '1';
      } else if (resource === 'returns') {
        remaining =
          line.remaining_return_quantity ?? line.accepted_quantity ?? line.quantity ?? '1';
      } else {
        remaining = line.accepted_quantity ?? line.quantity ?? '1';
      }
    }
    return {
      ...draft,
      // A source line is provenance, not the identity of the new target line.
      // The API assigns the target ID when this draft is created.
      id: undefined,
      quantity: trimDecimalZeros(remaining) || '0',
      sourceLineID: text(line.id) || undefined,
      sourceRemainingQuantity: trimDecimalZeros(remaining) || '0',
      purchaseOrderLineID:
        text(line.purchase_order_line_id) ||
        (!isSales && sourceResource === 'orders' ? text(line.id) : undefined),
      goodsReceiptLineID:
        text(line.goods_receipt_line_id) ||
        (!isSales && sourceResource === 'dispatches' ? text(line.id) : undefined)
    };
  }

  function sourceKindForResource(sourceResource: string) {
    if (sourceResource === 'quotes') return 'QUOTE';
    if (sourceResource === 'orders') return 'ORDER';
    if (sourceResource === 'dispatches') return isSales ? 'DISPATCH' : 'RECEIPT';
    if (sourceResource === 'invoices') return 'INVOICE';
    return 'DIRECT';
  }

  function hasConversionPermission(target: CommercialResource) {
    const targetConfig = commercialConfig(direction, target);
    return Boolean(
      targetConfig &&
      session &&
      (session.permissions.includes(targetConfig.managePermission) ||
        session.permissions.includes('commercial.document.manage'))
    );
  }

  function lineHasConversionQuantity(line: LineDraft, target: CommercialResource) {
    if (isSales) {
      const remaining =
        target === 'dispatches'
          ? line.remainingFulfillmentQuantity
          : target === 'invoices'
            ? line.remainingInvoicingQuantity
            : target === 'returns'
              ? line.sourceRemainingQuantity
              : undefined;
      return remaining !== undefined && decimalParts(remaining)[0] > 0n;
    }
    const ordered = line.orderedQuantity ?? line.quantity;
    const received = line.receivedQuantity ?? line.acceptedQuantity ?? '0';
    const invoiced = line.invoicedQuantity ?? '0';
    const remaining =
      target === 'dispatches'
        ? decimalSubtract(ordered, received)
        : line.lineType === 'SERVICE'
          ? decimalSubtract(ordered, invoiced)
          : decimalSubtract(received, invoiced);
    return decimalParts(remaining)[0] > 0n;
  }

  const conversionActions = $derived.by<ConversionAction[]>(() => {
    if (isNew || !record || !session) return [];
    const actions: ConversionAction[] = [];
    if (resource === 'orders') {
      if (
        (documentStatus === 'CONFIRMED' || documentStatus === 'PARTIALLY_FULFILLED') &&
        lines.some((line) => lineHasConversionQuantity(line, 'dispatches')) &&
        serverActionAllowed('can_create_dispatch') &&
        hasConversionPermission('dispatches')
      ) {
        actions.push({ target: 'dispatches', label: 'İrsaliye oluştur' });
      }
      if (
        (documentStatus === 'CONFIRMED' ||
          documentStatus === 'PARTIALLY_FULFILLED' ||
          documentStatus === 'FULFILLED') &&
        lines.some((line) => lineHasConversionQuantity(line, 'invoices')) &&
        serverActionAllowed('can_create_invoice') &&
        hasConversionPermission('invoices')
      ) {
        actions.push({ target: 'invoices', label: 'Fatura oluştur' });
      }
    } else if (resource === 'dispatches' && documentStatus === 'POSTED') {
      if (
        lines.some((line) => lineHasConversionQuantity(line, 'invoices')) &&
        serverActionAllowed('can_create_invoice') &&
        hasConversionPermission('invoices')
      ) {
        actions.push({ target: 'invoices', label: 'Fatura oluştur' });
      }
    } else if (isSales && resource === 'invoices' && documentStatus === 'POSTED') {
      if (
        serverActionAllowed('can_create_return') &&
        lines.some((line) => lineHasConversionQuantity(line, 'returns')) &&
        hasConversionPermission('returns')
      ) {
        actions.push({ target: 'returns', label: 'İade oluştur' });
      }
    }
    return actions;
  });

  function createFromCurrent(target: CommercialResource) {
    if (!record?.id) return;
    const targetPath = commercialPath(direction, target);
    void goto(
      `${targetPath}/yeni?source_id=${encodeURIComponent(record.id)}&source_resource=${encodeURIComponent(resource ?? '')}`
    );
  }

  async function loadSourceFromQuery() {
    if (!isNew) return;
    const sourceID = page.url.searchParams.get('source_id')?.trim();
    const sourceResource = page.url.searchParams.get('source_resource')?.trim();
    if (!sourceID || !sourceResource || !sourceResources().includes(sourceResource)) return;
    await selectSourceAndLoad({ id: sourceID, title: 'Seçili kaynak belge', kind: sourceResource });
  }

  function selectParty(option: PartyOption) {
    form.party = option;
  }

  function selectProduct(line: LineDraft, option: ProductOption) {
    if (option.kind !== (line.lineType === 'SERVICE' ? 'SERVICE' : 'PHYSICAL')) {
      validationError = `${line.lineType === 'SERVICE' ? 'Hizmet' : 'Ürün'} satırı için uyumlu kart seçin.`;
      return;
    }
    line.product = option;
    line.variant = null;
    line.variantSnapshot = false;
    line.variants = [];
    line.variantError = '';
    line.unitCode = option.unit || 'ADET';
    line.description = option.title;
    line.baseUnitPrice = option.baseUnitPrice || '';
    line.unitPrice = resolveLineUnitPrice(line.lineType, option.unitPrice, line.baseUnitPrice);
    line.taxRate = option.taxRate || '0';
    line.taxIncluded = option.taxIncluded ?? false;
    line.taxComponents = option.taxComponents ?? [];
    line.manualPrice = false;
    line.warehouse = line.lineType === 'PRODUCT' ? (line.warehouse ?? form.defaultWarehouse) : null;
    if (line.lineType === 'PRODUCT' && option.variantsEnabled) void loadVariantOptionsForLine(line);
  }

  function selectVariant(line: LineDraft, option: VariantOption) {
    line.variant = option;
    line.variantSnapshot = false;
    line.unitCode = option.unit || line.unitCode;
    line.baseUnitPrice = option.baseUnitPrice || line.product?.baseUnitPrice || '';
    line.unitPrice = priceFromBase(line.baseUnitPrice) || line.unitPrice;
    line.manualPrice = false;
    line.variantError = '';
  }

  function changeLineType(line: LineDraft, value: string) {
    line.lineType = value === 'SERVICE' ? 'SERVICE' : 'PRODUCT';
    if (line.lineType === 'SERVICE') {
      line.warehouse = null;
      line.product = line.product?.kind === 'SERVICE' ? line.product : null;
      line.variant = null;
      line.variants = [];
      line.variantError = '';
      line.baseUnitPrice = line.product?.baseUnitPrice || '';
      line.unitPrice = line.product
        ? resolveLineUnitPrice('SERVICE', line.product.unitPrice, line.baseUnitPrice)
        : '';
      line.unitCode = line.product?.unit || 'ADET';
      line.taxRate = line.product?.taxRate || '0';
      line.taxIncluded = line.product?.taxIncluded ?? false;
      line.taxComponents = line.product?.taxComponents ?? [];
      line.description = line.product?.title || 'Hizmet';
    } else {
      line.warehouse = line.warehouse ?? form.defaultWarehouse;
      line.product = line.product?.kind === 'PHYSICAL' ? line.product : null;
      line.variant = line.product ? line.variant : null;
      line.variants = line.product ? line.variants : [];
      line.variantError = '';
      line.baseUnitPrice = line.product?.baseUnitPrice || '';
      line.unitCode = line.product?.unit || 'ADET';
      line.taxRate = line.product?.taxRate || '0';
      line.taxIncluded = line.product?.taxIncluded ?? false;
      line.taxComponents = line.product?.taxComponents ?? [];
      line.unitPrice = line.product?.unitPrice || (line.product ? line.unitPrice : '');
      line.description = line.product?.title || '';
    }
  }

  function addLine(type: CommercialLineType) {
    lines = [...lines, emptyLine(type)];
  }

  function removeLine(index: number) {
    lines = lines.filter((_, lineIndex) => lineIndex !== index);
  }

  function payload() {
    return buildDocumentPayload({ form, lines, isSales, isPurchaseOrder, resource, currency });
  }

  function checkForm(requireLines = false) {
    if (sourceError) return sourceError;
    if (!form.party?.id) return 'Cari seçilmelidir.';
    if (!form.branchID) return 'Şube bilgisi bulunamadı. Erişilebilir bir depo seçin.';
    if (!form.documentDate) return 'Belge tarihi gereklidir.';
    if (requireLines && !lines.length) return 'Belge en az bir satır içermelidir.';
    for (const [index, line] of lines.entries()) {
      if (!line.quantity || line.quantity === '0') return `${index + 1}. satır miktarı gereklidir.`;
      if (
        line.sourceRemainingQuantity &&
        decimalParts(line.quantity)[0] > decimalParts(line.sourceRemainingQuantity)[0]
      )
        return `${index + 1}. satır miktarı kaynaktaki kalan miktarı aşamaz (${formatQuantityWithUnit(line.sourceRemainingQuantity, line.unitCode)}).`;
      if (!isLineTypeAllowed(line.lineType)) return `${index + 1}. satırda hizmet kullanılamaz.`;
      if (isSales && line.lineType === 'PRODUCT' && !line.product)
        return `${index + 1}. satır için ürün seçin.`;
      if (!isSales && !line.product)
        return `${index + 1}. satır için ${line.lineType === 'SERVICE' ? 'hizmet' : 'ürün'} kartı seçin.`;
      if (
        line.product &&
        line.product.kind !== (line.lineType === 'SERVICE' ? 'SERVICE' : 'PHYSICAL')
      )
        return `${index + 1}. satır kartı türüyle eşleşmiyor.`;
      if (line.lineType === 'PRODUCT' && line.product?.variantsEnabled && line.variantLoading)
        return `${index + 1}. satır varyantları yükleniyor.`;
      if (line.lineType === 'PRODUCT' && line.product?.variantsEnabled && !line.variant)
        return `${index + 1}. satır için varyant seçin.`;
      if (line.lineType === 'PRODUCT' && !line.warehouse?.id)
        return `${index + 1}. satır için depo seçin.`;
      if (line.lineType === 'SERVICE' && line.warehouse)
        return `${index + 1}. hizmet satırında depo bulunamaz.`;
      if (line.unitPrice === '') return `${index + 1}. satır fiyatı gereklidir.`;
    }
    if (requiresHeaderWarehouse && !form.defaultWarehouse?.id) return 'Depo seçilmelidir.';
    if (referencesError) return 'Depo listesi alınamadı. Yeniden deneyin.';
    if (isSales && resource === 'returns' && !form.sourceDocumentID)
      return 'Satış iadesi için kaynak belge seçilmelidir.';
    if (resource === 'returns' && !form.reason.trim()) return 'İade gerekçesi gereklidir.';
    return '';
  }

  async function save(options: { chain?: boolean } = {}): Promise<DocumentRecord | null> {
    validationError = checkForm(false);
    if (validationError) {
      setTimeout(() => validationElement?.focus(), 0);
      return null;
    }
    if (!config || !isEditable || saving || submitting || sourceLoading || sourceError) return null;
    saving = true;
    submitting = true;
    error = '';
    serverErrors = [];
    staleConflict = false;
    try {
      const result = await api<DocumentRecord>(
        isNew ? endpoint : `${endpoint}/${encodeURIComponent(record?.id ?? page.params.id ?? '')}`,
        {
          method: isNew ? 'POST' : 'PUT',
          headers: isNew ? undefined : { 'If-Match': `"${record?.version ?? 1}"` },
          body: JSON.stringify(payload())
        }
      );
      if (isNew && result.id && !options.chain) {
        navigationInProgress = true;
        await goto(`${routePath}/${encodeURIComponent(result.id)}`);
      } else {
        hydrate(result);
        if (!options.chain) toast.success('Taslak kaydedildi.');
      }
      return result;
    } catch (cause) {
      error = friendlyError(cause, 'Belge kaydedilemedi.');
      setServerErrorState(cause);
      return null;
    } finally {
      saving = false;
      submitting = false;
    }
  }

  // Combined primary action: persist the draft and immediately run its next
  // lifecycle command (send / accept / confirm / finalize) so the common
  // "kaydet + onayla" path is a single click. The plain "Taslağı kaydet"
  // button stays available for stopping at draft.
  async function saveAndAdvance() {
    const command = commandForPrimary();
    if (!command) return;
    const wasNew = isNew;
    const saved = await save({ chain: true });
    if (!saved?.id) return;
    await runCommand(command);
    if (wasNew && record?.id) {
      navigationInProgress = true;
      await goto(`${routePath}/${encodeURIComponent(record.id)}`);
    }
  }

  function primaryCommandLabel(command: string | undefined): string {
    switch (command) {
      case 'send':
        return 'gönder';
      case 'accept':
        return 'kabul et';
      case 'confirm':
        return 'onayla';
      case 'finalize':
        return resource === 'dispatches'
          ? 'sonlandır'
          : resource === 'invoices'
            ? 'faturayı sonlandır'
            : resource === 'returns'
              ? 'iadeyi sonlandır'
              : 'sonlandır';
      default:
        return 'işle';
    }
  }

  // A posted (or confirmed order / sent quote) document can be cancelled when
  // the server's action matrix allows it. Cancellation reverses the document's
  // stock/finance effects with append-only reversal records and recomputes the
  // upstream document's derived status; the backend blocks it when a finalized
  // downstream document still depends on this one.
  const canCancel = $derived(
    !isNew && canManage && Boolean(record?.id) && serverActionAllowed('can_cancel', false)
  );

  // The server clears can_cancel when a finalized downstream document depends on
  // this one; show the user why the action is unavailable.
  const cancelBlockedByDownstream = $derived(
    !isNew &&
      canManage &&
      documentStatus === 'POSTED' &&
      !serverActionAllowed('can_cancel', false) &&
      (resource === 'dispatches' || resource === 'invoices') &&
      Boolean(record?.related_documents?.some((related) => related.status === 'POSTED'))
  );

  // The server also clears can_cancel for a (partially) collected/paid invoice;
  // the collections/payments must be reversed first.
  const blockingPayments = $derived(record?.settlement?.payments ?? []);
  const cancelBlockedByPayment = $derived(
    !isNew &&
      canManage &&
      documentStatus === 'POSTED' &&
      resource === 'invoices' &&
      !serverActionAllowed('can_cancel', false) &&
      (record?.settlement?.payment_status === 'PAID' ||
        record?.settlement?.payment_status === 'PARTIALLY_PAID')
  );

  function paymentDetailPath(payment: { id: string; payment_kind?: string }) {
    const base = payment.payment_kind === 'COLLECTION' ? '/cari/tahsilatlar' : '/cari/odemeler';
    return `${base}/${encodeURIComponent(payment.id)}`;
  }

  const cancelEffectPreview = $derived.by(() => {
    const lines: string[] = [];
    if (isSales) {
      if (resource === 'dispatches')
        lines.push(
          'Sevk edilen miktar stoğa geri eklenecek, siparişin sevk durumu yeniden hesaplanacak.'
        );
      else if (resource === 'invoices')
        lines.push(
          'Fatura cari/finans hareketi ters kaydedilecek; irsaliyesiz fatura ise stok da geri eklenecek.'
        );
      else if (resource === 'returns')
        lines.push(
          'İade stok ve cari hareketi ters kaydedilecek, iade edilebilir miktar yeniden açılacak.'
        );
      else if (resource === 'orders') lines.push('Sipariş rezervasyonları serbest bırakılacak.');
    } else {
      if (resource === 'dispatches')
        lines.push(
          'Teslim alınan miktar stoktan düşülecek, siparişin karşılama durumu yeniden hesaplanacak.'
        );
      else if (resource === 'invoices')
        lines.push(
          'Fatura cari/finans hareketi ters kaydedilecek; mal kabulsüz fatura ise stok da düşülecek.'
        );
      else if (resource === 'returns') lines.push('İade stok ve cari hareketi ters kaydedilecek.');
    }
    lines.push('Belge geçmişte "İptal" olarak korunacak; kayıt silinmez.');
    return lines.join(' ');
  });

  function commandForPrimary(): string | undefined {
    if (!canManage || !serverActionAllowed('can_post')) return undefined;
    if (isSales) {
      if (resource === 'quotes')
        return documentStatus === 'DRAFT'
          ? 'send'
          : documentStatus === 'SENT'
            ? 'accept'
            : undefined;
      if (resource === 'orders') return documentStatus === 'DRAFT' ? 'confirm' : undefined;
      if (resource && ['dispatches', 'invoices', 'returns'].includes(resource)) {
        return documentStatus === 'DRAFT' ? 'finalize' : undefined;
      }
    }
    if (isPurchaseOrder && documentStatus === 'DRAFT') return 'confirm';
    if (!isSales && resource && ['dispatches', 'invoices', 'returns'].includes(resource)) {
      return documentStatus === 'DRAFT' ? 'finalize' : undefined;
    }
    return undefined;
  }

  function commandIdempotencyKey(
    documentID: string,
    command: string,
    version: number,
    reason: string
  ) {
    const scope = `${documentID}:${command}:${version}:${reason}`;
    const current = commandKeys.get(scope);
    if (current) return current;
    const generated =
      globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`;
    commandKeys.set(scope, generated);
    return generated;
  }

  function commandSuccessMessage(command: string): string {
    if (command === 'confirm') return 'Belge onaylandı.';
    if (command === 'finalize' || command === 'post') return 'Belge sonlandırıldı.';
    if (command === 'cancel') return 'Belge iptal edildi.';
    return 'Belge durumu güncellendi.';
  }

  async function waitForCommandResult(
    documentID: string,
    expectedVersion: number,
    previousStatus: string
  ) {
    for (let attempt = 0; attempt < 5; attempt += 1) {
      await new Promise((resolve) => setTimeout(resolve, 400));
      try {
        const latest = await api<DocumentRecord>(`${endpoint}/${encodeURIComponent(documentID)}`);
        if (
          (latest.version ?? expectedVersion) > expectedVersion ||
          latest.status !== previousStatus
        )
          return latest;
      } catch {
        // The original command result remains authoritative; a failed refresh
        // must not turn a successful post into a second mutation attempt.
      }
    }
    return null;
  }

  async function runCommand(command: string, reason = '') {
    if (!record?.id || !config || submitting || saving) return;
    if (command === 'cancel' ? !canCancel : !commandForPrimary()) return;
    if ((command === 'post' || command === 'confirm' || command === 'finalize') && !lines.length) {
      validationError = 'Belgeyi kesinleştirmeden önce en az bir satır ekleyin.';
      setTimeout(() => validationElement?.focus(), 0);
      return;
    }
    const documentID = record.id;
    const expectedVersion = record.version ?? 1;
    const previousStatus = documentStatus;
    submitting = true;
    error = '';
    serverErrors = [];
    staleConflict = false;
    try {
      const result = await api<DocumentRecord>(
        `${endpoint}/${encodeURIComponent(documentID)}/${command}`,
        {
          method: 'POST',
          headers: {
            'If-Match': `"${expectedVersion}"`,
            'Idempotency-Key': commandIdempotencyKey(documentID, command, expectedVersion, reason)
          },
          body: JSON.stringify(command === 'cancel' ? { reason } : {})
        }
      );
      hydrate(result);
      toast.success(commandSuccessMessage(command));
    } catch (cause) {
      if (cause instanceof APIRequestError && cause.code === 'COMMAND_IN_PROGRESS') {
        const latest = await waitForCommandResult(documentID, expectedVersion, previousStatus);
        if (latest) {
          hydrate(latest);
          toast.success(commandSuccessMessage(command));
          submitting = false;
          return;
        }
      }
      error = friendlyError(cause, 'Belge işlemi tamamlanamadı.');
      setServerErrorState(cause);
    } finally {
      submitting = false;
    }
  }

  async function printPage() {
    if (typeof window === 'undefined') return;
    if (!config || !record?.id || isNew) {
      window.print();
      return;
    }
    try {
      const partyID = text(record.party_id ?? record.supplier_id);
      if (partyID && partyDetail?.id !== partyID) {
        try {
          partyDetail = await getParty(partyID);
        } catch {
          partyDetail = undefined;
        }
      }
      // The session's company record carries no logo or tax number; the printed
      // header wants both.
      const profile = (await printableCompany()) ?? company;
      printDocument(
        buildCommercialDocument({
          config,
          record,
          lines,
          currency,
          totals: {
            subtotal: text(displaySubtotal, '0'),
            discount: text(displayDiscountTotal, '0'),
            tax: text(displayTaxTotal, '0'),
            vat: text(displayVatTotal, '0'),
            additionalTax: text(displayAdditionalTaxTotal, '0'),
            grand: text(displayGrandTotal, '0')
          },
          company: profile,
          party: partyDetail
        })
      );
    } catch (cause) {
      toast.error(friendlyError(cause, 'Fiş oluşturulamadı.'));
      window.print();
    }
  }

  async function deleteDraft() {
    if (
      !record?.id ||
      !config ||
      submitting ||
      saving ||
      !isEditable ||
      !serverActionAllowed('can_delete', false)
    )
      return;
    submitting = true;
    try {
      await api<void>(`${endpoint}/${encodeURIComponent(record.id)}`, {
        method: 'DELETE',
        headers: { 'If-Match': `"${record.version ?? 1}"` }
      });
      navigationInProgress = true;
      toast.success('Taslak silindi.');
      await goto(routePath);
    } catch (cause) {
      error = friendlyError(cause, 'Taslak silinemedi.');
      setServerErrorState(cause);
    } finally {
      submitting = false;
    }
  }

  function setServerErrorState(cause: unknown) {
    if (!(cause instanceof APIRequestError)) return;
    staleConflict = cause.code === 'DOCUMENT_MODIFIED';
    const details = cause.details;
    const entries: unknown[] = [];
    if (details && (details.field || details.line || details.message)) entries.push(details);
    for (const key of ['errors', 'violations', 'field_errors']) {
      const value = details?.[key];
      if (Array.isArray(value)) entries.push(...value);
    }
    serverErrors = entries
      .map((entry) => serverErrorDetail(entry))
      .filter((entry): entry is ServerErrorDetail => Boolean(entry));
  }

  function serverErrorDetail(value: unknown): ServerErrorDetail | null {
    if (!value || typeof value !== 'object') return null;
    const entry = value as Record<string, unknown>;
    const rawField = text(entry.field ?? entry.path ?? entry.name).trim();
    const rawLine = entry.line ?? entry.line_no ?? entry.lineNumber;
    let line = Number(rawLine);
    if (!Number.isInteger(line) || line < 1) {
      const match = rawField.match(/(?:lines?|items?)[.[\]]?(\d+)/i);
      line = match ? Number(match[1]) + 1 : NaN;
    }
    const field = rawField
      .split(/[.[\]]/)
      .filter(Boolean)
      .at(-1);
    const message = text(entry.message ?? entry.detail ?? entry.error).trim();
    if (!message && !rawField) return null;
    return {
      line: Number.isInteger(line) && line > 0 ? line : undefined,
      field: field || undefined,
      message: message || 'Alanı kontrol edin.'
    };
  }

  function lineError(lineIndex: number, field?: string) {
    return (
      serverErrors.find(
        (entry) => entry.line === lineIndex + 1 && (!field || !entry.field || entry.field === field)
      )?.message ?? ''
    );
  }

  function focusServerError(detail: ServerErrorDetail) {
    const fieldIDs: Record<string, string> = {
      party_id: 'document-party',
      supplier_id: 'document-party',
      branch_id: 'document-branch',
      document_date: 'document-date',
      due_date: 'document-due-date',
      currency_code: 'document-currency',
      source_document_id: 'source-document'
    };
    const lineFieldIDs: Record<string, string> = {
      product_id: 'product',
      variant_id: 'variant',
      warehouse_id: 'warehouse',
      quantity: 'quantity',
      unit_price: 'unit-price',
      discount_rate: 'discount-rate',
      tax_rate: 'tax-rate'
    };
    const id = detail.line
      ? `line-${detail.line}-${lineFieldIDs[detail.field ?? ''] ?? 'quantity'}`
      : fieldIDs[detail.field ?? ''];
    const target = id ? document.getElementById(id) : undefined;
    const row = detail.line
      ? document.querySelector<HTMLElement>(`[data-line-number="${detail.line}"]`)
      : undefined;
    (target ?? row ?? validationElement)?.scrollIntoView({ block: 'center' });
    (target ?? row ?? validationElement)?.focus();
  }

  async function refreshAfterConflict() {
    staleConflict = false;
    serverErrors = [];
    await load();
  }

  function friendlyError(cause: unknown, fallback: string) {
    if (cause instanceof APIRequestError) {
      const messages: Record<string, string> = {
        INSUFFICIENT_AVAILABLE_STOCK: 'Kullanılabilir stok yetersiz.',
        ORDER_LINE_OVER_FULFILLMENT: 'Sipariş satırı miktarı aşılamaz.',
        DOCUMENT_ALREADY_POSTED: 'Belge daha önce sonlandırıldı.',
        DOCUMENT_NOT_EDITABLE: 'Bu belge artık düzenlenemez.',
        DOCUMENT_PERIOD_LOCKED: 'Belge tarihi kilitli dönemdedir.',
        WAREHOUSE_REQUIRED: 'Ürün satırı için depo gereklidir.',
        WAREHOUSE_NOT_AUTHORIZED: 'Seçilen depoya erişim yetkiniz yok.',
        PRICE_REQUIRED: 'Satır fiyatı bulunamadı.',
        TAX_PROFILE_INVALID: 'Vergi profili geçersiz.',
        EXCHANGE_RATE_UNAVAILABLE: 'Belge para birimi için güncel kur alınamadı.',
        VARIANT_REQUIRED: 'Varyantlı ürün için varyant seçilmelidir.',
        PAYMENT_INTEGRATION_UNAVAILABLE: 'Finans kaydı oluşturulamadı.',
        RETURN_REASON_REQUIRED: 'İade gerekçesi gereklidir.',
        INVALID_DOCUMENT_RELATION: 'Kaynak belge, satır veya miktar ilişkisini kontrol edin.',
        DOCUMENT_MODIFIED:
          'Bu belge başka bir kullanıcı tarafından değiştirildi. Güncel hali yükleyin.',
        INVALID_STATE_TRANSITION: 'Belge durumu bu işlem için uygun değil.',
        CALCULATION_CHANGED:
          'Belge toplamları yeniden hesaplandı. Lütfen güncel değerleri kontrol edin.',
        PRODUCT_INACTIVE: 'Seçilen ürün kartı artık aktif değil.',
        PARTY_INACTIVE: 'Seçilen cari artık aktif değil.',
        WAREHOUSE_INACTIVE: 'Seçilen depo artık aktif değil.',
        SOURCE_ALREADY_CONSUMED: 'Kaynak belgenin kullanılabilir miktarı kalmadı.',
        SOURCE_PARTY_MISMATCH: 'Kaynak belge farklı bir cari içeriyor.',
        SOURCE_CURRENCY_MISMATCH: 'Kaynak belge farklı bir para birimi içeriyor.',
        DOCUMENT_HAS_NO_LINES: 'Belgeyi kesinleştirmeden önce en az bir satır ekleyin.',
        EXCHANGE_RATE_REQUIRED: 'Belgeyi kesinleştirmek için güncel kur gereklidir.',
        COMMAND_IN_PROGRESS:
          'Belge işlemi tamamlanıyor. Belge durumu yenilenemedi; kısa süre sonra tekrar deneyin.'
      };
      const mapped = messages[cause.code];
      if (mapped) return mapped;
      // Sunucu tarafı hatalarında (5xx / INTERNAL_ERROR) ham mesaj çoğu zaman
      // kullanıcıya bir şey anlatmaz; izlenebilir olması için trace kimliğini ekle.
      if (
        cause.status >= 500 ||
        cause.code === 'INTERNAL_ERROR' ||
        cause.code === 'REQUEST_FAILED'
      ) {
        const base = cause.message && cause.message !== fallback ? cause.message : fallback;
        return cause.traceId
          ? `${base} Sorun sürerse şu referansı bildirin: ${cause.traceId}`
          : base;
      }
      return cause.message ?? fallback;
    }
    return cause instanceof Error && cause.message ? cause.message : fallback;
  }

  function lineDisplayTotal(line: LineDraft) {
    if (!isEditable && line.persistedTotal) return formatMoney(line.persistedTotal, currency);
    return formatMoney(lineAmounts(line).total, currency);
  }

  /** Every tax the line carries besides KDV, labelled with its rate and what
   *  it adds to the line. Empty for a line that only carries KDV. */
  function lineTaxBreakdown(line: LineDraft) {
    return lineComponentAmounts(line).map((component) => ({
      label: componentLabel(component, line.unitCode),
      amount: formatMoney(component.amount, currency)
    }));
  }

  function componentLabel(component: LineTaxComponent, unitCode: string) {
    const name = component.name || component.code;
    if (component.calculationType === 'PERCENTAGE') {
      return `${name} %${formatQuantity(component.rate)}`;
    }
    if (component.calculationType === 'QUANTITY_BASED') {
      return `${name} ${formatMoney(component.rate, currency)}/${unitCode || 'ADET'}`;
    }
    return `${name} ${formatMoney(component.rate, currency)}`;
  }

  function lineProgressText(line: LineDraft) {
    if (isNew || resource !== 'orders') return '';
    const ordered = line.orderedQuantity ?? line.quantity;
    const remaining = isSales
      ? line.remainingFulfillmentQuantity
      : line.lineType === 'SERVICE'
        ? decimalSubtract(ordered, line.invoicedQuantity ?? '0')
        : decimalSubtract(ordered, line.receivedQuantity ?? line.acceptedQuantity ?? '0');
    if (remaining === undefined || ordered === undefined) return '';
    const fulfilled = decimalSubtract(ordered, remaining);
    const completedLabel = isSales
      ? 'Karşılanan'
      : line.lineType === 'SERVICE'
        ? 'Faturalanan'
        : 'Teslim alınan';
    return `${completedLabel}: ${formatQuantityWithUnit(fulfilled, line.unitCode)} · Kalan: ${formatQuantityWithUnit(remaining, line.unitCode)}`;
  }

  function invoiceStockEffectText() {
    if (resource !== 'invoices' || documentStatus !== 'POSTED') return '';
    if (isSales) {
      return form.sourceKind === 'DISPATCH'
        ? 'Stok etkisi: Kaynak irsaliyede oluşturuldu; bu fatura stoktan tekrar düşmez.'
        : 'Stok etkisi: Bu fatura sonlandırıldığında stoktan düşüldü.';
    }
    return form.sourceKind === 'RECEIPT'
      ? 'Stok etkisi: Kaynak mal kabulde oluşturuldu; bu fatura stoğa tekrar eklenmez.'
      : 'Stok etkisi: Bu fatura sonlandırıldığında stoğa eklendi.';
  }

  const formTotals = $derived(computeFormTotals(lines));
  const formTotal = $derived(formTotals.payableTotal);
  const serverGrandTotal = $derived(record?.grand_total ?? record?.payable_total ?? record?.total);
  const displaySubtotal = $derived(
    !isEditable && record?.subtotal ? record.subtotal : formTotals.subtotal
  );
  const displayDiscountTotal = $derived(
    !isEditable && record?.discount_total ? record.discount_total : formTotals.discountTotal
  );
  const displayTaxTotal = $derived(
    !isEditable && record?.tax_total ? record.tax_total : formTotals.taxTotal
  );
  // Additional taxes always come from the lines: a posted document carries its
  // own component breakdown, so the KDV row is what is left of the tax total.
  const displayAdditionalTaxTotal = $derived(formTotals.additionalTaxTotal);
  const displayVatTotal = $derived(
    !isEditable && record?.tax_total
      ? decimalSubtract(text(record.tax_total), formTotals.additionalTaxTotal)
      : formTotals.vatTotal
  );
  const displayGrandTotal = $derived(
    !isEditable && serverGrandTotal ? serverGrandTotal : formTotal
  );

  async function initialize() {
    if (!config) return;
    try {
      session = await api<Session>('/session');
      company = session.companies.find((item) => item.id === session?.current_company_id);
      baseCurrency = company?.base_currency || 'TRY';
      exchangeRates = { [baseCurrency]: '1' };
      if (isNew) {
        form.currency = baseCurrency;
        form.exchangeRate = '1';
      }
      const hasRead =
        session.permissions.includes(config.permission) ||
        session.permissions.includes(config.managePermission) ||
        session.permissions.includes('commercial.document.manage') ||
        session.permissions.includes('commercial.document.read');
      denied = isNew ? !canDraft : !hasRead;
      if (denied) {
        loading = false;
        return;
      }
      void loadCurrencyReferences();
      if (isNew) {
        await loadReferences();
        await loadSourceFromQuery();
        loading = false;
        markClean();
        setTimeout(() => document.getElementById('document-no')?.focus(), 0);
      } else {
        await load();
        await loadReferences();
      }
    } catch (cause) {
      error = friendlyError(cause, 'Oturum bilgileri alınamadı.');
      loading = false;
    }
  }

  onMount(() => {
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      if (!shouldPromptForUnsavedChanges()) return;
      event.preventDefault();
      event.returnValue = '';
    };
    const handleEditorKeydown = (event: KeyboardEvent) => {
      if (
        !isEditable ||
        event.defaultPrevented ||
        event.isComposing ||
        !(event.ctrlKey || event.metaKey) ||
        event.key.toLowerCase() !== 's' ||
        (event.target instanceof Element && event.target.closest('[role="dialog"]'))
      )
        return;
      event.preventDefault();
      void save();
    };
    const removeShortcut = listenVaryaShortcut('save', () => {
      if (isEditable) void save();
    });
    window.addEventListener('beforeunload', handleBeforeUnload);
    window.addEventListener('keydown', handleEditorKeydown);
    void initialize();
    return () => {
      removeShortcut();
      window.removeEventListener('beforeunload', handleBeforeUnload);
      window.removeEventListener('keydown', handleEditorKeydown);
    };
  });
</script>

<svelte:head
  ><title
    >{isNew ? `Yeni ${config?.title ?? 'Belge'}` : `${config?.title ?? 'Belge'} ${headerNumber}`} · Varya
    One</title
  ></svelte:head
>

{#if config}
  <DocumentToolbar
    title={isNew
      ? `Yeni ${config.title}`
      : `${config.title}${headerNumber ? ` · ${headerNumber}` : ''}`}
    subtitle={config.subtitle}
    mode="document"
  >
    {#snippet status()}
      {#if !isNew}
        <div class="status-stack" aria-label="Belge durumları">
          <span
            class={`status-pill ${documentStatusTone(record?.lifecycle_status ?? documentStatus)}`}
            >{axisLabel('lifecycle_status', record?.lifecycle_status ?? documentStatus)}</span
          >
          {#if record?.fulfillment_status}
            <span class={`status-pill ${documentStatusTone(record.fulfillment_status)}`}
              >Karşılama: {axisLabel('fulfillment_status', record.fulfillment_status)}</span
            >
          {/if}
          {#if record?.invoicing_status}
            <span class={`status-pill ${documentStatusTone(record.invoicing_status)}`}
              >Faturalama: {axisLabel('invoicing_status', record.invoicing_status)}</span
            >
          {/if}
          {#if record?.payment_status}
            <span class={`status-pill ${documentStatusTone(record.payment_status)}`}
              >Ödeme: {axisLabel('payment_status', record.payment_status)}</span
            >
          {/if}
        </div>
        {#if record?.fulfillment_at}
          <p class="fulfillment-time">
            Karşılama zamanı: {formatDate(record.fulfillment_at, true)}
          </p>
        {/if}
      {/if}
    {/snippet}
    {#snippet primary()}
      <Button variant="outline" onclick={() => goto(routePath)}><ArrowLeft size={14} />Liste</Button
      >
      {#if isEditable && commandForPrimary() && (isNew || isDirty)}
        <Button
          disabled={saving || submitting || sourceLoading || anyVariantLoading()}
          onclick={saveAndAdvance}
          ><Check size={14} />{saving || submitting
            ? 'İşleniyor…'
            : `Kaydet ve ${primaryCommandLabel(commandForPrimary())}`}</Button
        >
      {/if}
      {#if isEditable && (isNew || isDirty || !commandForPrimary())}
        <Button
          variant={commandForPrimary() ? 'outline' : 'default'}
          disabled={saving || submitting || sourceLoading || anyVariantLoading()}
          onclick={() => void save()}
          ><Save size={14} />{saving
            ? 'Kaydediliyor…'
            : config.isDraft
              ? 'Taslağı kaydet'
              : 'Belgeyi kaydet'}</Button
        >
      {/if}
      {#if commandForPrimary() && !isNew && !(isEditable && isDirty)}
        <Button disabled={submitting || saving} onclick={() => runCommand(commandForPrimary()!)}>
          <Check size={14} />{commandForPrimary() === 'send'
            ? 'Gönder'
            : commandForPrimary() === 'accept'
              ? 'Kabul et'
              : commandForPrimary() === 'confirm'
                ? 'Onayla'
                : commandForPrimary() === 'finalize'
                  ? resource === 'dispatches'
                    ? 'İrsaliyeyi sonlandır'
                    : resource === 'invoices'
                      ? 'Faturayı sonlandır'
                      : resource === 'returns'
                        ? 'İadeyi sonlandır'
                        : 'Belgeyi sonlandır'
                  : 'Belgeyi güncelle'}
        </Button>
      {/if}
      {#each conversionActions as action}
        <Button
          variant="outline"
          disabled={submitting || saving}
          onclick={() => createFromCurrent(action.target)}>{action.label}</Button
        >
      {/each}
    {/snippet}
    {#snippet tools()}
      {#if !isNew && isEditable}
        <Button
          variant="danger"
          size="sm"
          disabled={submitting || saving}
          onclick={() => (deleteConfirmOpen = true)}><Trash2 size={14} />Taslağı sil</Button
        >
      {/if}
      {#if canCancel}
        <Button
          variant="danger"
          size="sm"
          disabled={submitting || saving}
          onclick={() => (cancelDialogOpen = true)}><Ban size={14} />Belgeyi iptal et</Button
        >
      {:else if cancelBlockedByDownstream}
        <span class="read-only-note"
          >Bu belge kesinleşmiş bir bağlı belgede kullanıldığı için iptal edilemez; önce bağlı
          belgeyi iptal edin.</span
        >
      {:else if cancelBlockedByPayment}
        <span class="read-only-note cancel-blocked-note">
          {record?.settlement?.payment_status === 'PARTIALLY_PAID'
            ? 'Bu fatura kısmen tahsil edildiği için iptal edilemez.'
            : 'Bu fatura tahsil edildiği için iptal edilemez.'}
          Önce ilgili {isSales ? 'tahsilatı' : 'ödemeyi'} ters kaydedin:
          {#if blockingPayments.length}
            <span class="cancel-blocked-links">
              {#each blockingPayments as payment, index}
                {#if index > 0},
                {/if}<a href={paymentDetailPath(payment)}>{payment.document_no || 'Kayıt'}</a>
              {/each}
            </span>
          {/if}
        </span>
      {/if}
      {#if !isNew}
        <Button variant="outline" size="sm" onclick={printPage}><Printer size={14} />Yazdır</Button>
      {/if}
      {#if !isNew && documentStatus === 'POSTED'}
        {#if canOpenFinanceAction}<Button
            variant="outline"
            size="sm"
            disabled={submitting || saving}
            onclick={() => void goto(financeActionPath())}
            ><WalletCards size={14} />{isSales ? 'Tahsilat oluştur' : 'Ödeme oluştur'}</Button
          >{/if}
        <span class="read-only-note"
          >Sonlandırılmış belge salt okunur. Düzeltme için iade veya ters kayıt akışını kullanın.</span
        >
      {/if}
    {/snippet}
  </DocumentToolbar>

  {#if denied}
    <section class="permission-card" role="alert">
      <strong>Bu ekran için yetkiniz yok.</strong>
      <span>Belge bilgileri şirket ve yetki kapsamınız içinde gösterilir.</span>
    </section>
  {:else if loading}
    <StateBlock loading loadingText="Belge hazırlanıyor…" />
  {:else if error && !record && !isNew}
    <StateBlock {error} onRetry={load} />
  {:else}
    {#if error}<div class="form-error" role="alert">
        <span>{error}</span>
        {#if staleConflict}
          <Button size="sm" variant="outline" onclick={() => void refreshAfterConflict()}
            >Belgeyi yenile</Button
          >
        {/if}
      </div>{/if}
    {#if referencesError}
      <div class="form-error reference-error" role="alert">
        <strong>Depolar yüklenemedi</strong>
        <span>{referencesError}</span>
        <Button size="sm" variant="outline" disabled={referencesLoading} onclick={loadReferences}
          >{referencesLoading ? 'Yükleniyor…' : 'Tekrar dene'}</Button
        >
      </div>
    {/if}
    {#if sourceError}<div class="form-error" role="alert">{sourceError}</div>{/if}
    {#if serverErrors.length}
      <div class="form-error error-summary" role="alert" aria-labelledby="server-error-title">
        <strong id="server-error-title">Belge kaydedilemedi</strong>
        <ul>
          {#each serverErrors as detail}
            <li>
              <button type="button" class="error-link" onclick={() => focusServerError(detail)}>
                {detail.line
                  ? `${detail.line}. satır${detail.field ? ` · ${detail.field}` : ''}: `
                  : ''}{detail.message}
              </button>
            </li>
          {/each}
        </ul>
      </div>
    {/if}
    {#if validationError && !(validationError.endsWith('varyantları yükleniyor.') && !anyVariantLoading())}
      <div bind:this={validationElement} class="form-error" role="alert" tabindex="-1">
        {validationError}
      </div>
    {/if}

    <div class="document-layout">
      <section class="document-card" aria-labelledby="document-header-title">
        <div class="section-heading">
          <div>
            <span class="section-kicker">01 · Belge başlığı</span>
            <h2 id="document-header-title">Cari ve belge bilgileri</h2>
          </div>
        </div>
        <div class="form-grid">
          <Field.Field>
            <Field.Label for="document-party"
              >{isSales ? 'Cari' : 'Tedarikçi'} <span>*</span></Field.Label
            >
            <EntityCombobox
              id="document-party"
              selected={form.party}
              title={isSales ? 'Müşteri cari seç' : 'Tedarikçi seç'}
              description="Yetki kapsamınızdaki aktif carilerden birini seçin."
              triggerLabel={isSales ? 'Müşteri cari' : 'Tedarikçi'}
              triggerPlaceholder="Cari kodu veya unvan ara…"
              searchPlaceholder="Cari kodu veya unvan ara…"
              onSearch={searchParties}
              minQueryLength={0}
              advancedSearch
              disabled={editorMutationDisabled}
              onSelect={selectParty}
            />
          </Field.Field>
          <Field.Field>
            <Field.Label for="document-no">Belge No</Field.Label>
            <Input
              id="document-no"
              bind:value={form.documentNo}
              disabled={editorMutationDisabled}
              placeholder="Boş bırakırsanız otomatik üretilir"
            />
          </Field.Field>
          <Field.Field>
            <Field.Label for="document-date">Belge Tarihi <span>*</span></Field.Label>
            <DateInput
              id="document-date"
              ariaLabel="Belge tarihi"
              bind:value={form.documentDate}
              disabled={editorMutationDisabled}
              required
            />
          </Field.Field>
          <Field.Field>
            <Field.Label for="document-currency">Para Birimi <span>*</span></Field.Label>
            <select
              id="document-currency"
              value={form.currency}
              disabled={editorMutationDisabled}
              onchange={(event) =>
                void changeCurrency((event.currentTarget as HTMLSelectElement).value)}
            >
              {#if currencyOptions.length}
                {#each currencyOptions.filter((item) => item.is_active) as item}<option
                    value={item.code}>{item.code} · {item.name}</option
                  >{/each}
              {:else}
                <option value="TRY">TRY · Türk Lirası</option>
                <option value="USD">USD · Amerikan Doları</option>
                <option value="EUR">EUR · Avro</option>
                <option value="GBP">GBP · İngiliz Sterlini</option>
              {/if}
            </select>
          </Field.Field>
          {#if isSales || resource === 'invoices'}
            <Field.Field>
              <Field.Label for="document-due-date">Vade Tarihi</Field.Label>
              <DateInput
                id="document-due-date"
                ariaLabel="Vade tarihi"
                bind:value={form.dueDate}
                disabled={editorMutationDisabled}
              />
            </Field.Field>
          {/if}
          {#if resource === 'quotes'}
            <Field.Field>
              <Field.Label for="document-valid-until">Geçerlilik Tarihi</Field.Label>
              <DateInput
                id="document-valid-until"
                ariaLabel="Teklif geçerlilik tarihi"
                bind:value={form.validUntil}
                disabled={editorMutationDisabled}
              />
            </Field.Field>
          {/if}
          <Field.Field>
            <Field.Label for="document-warehouse"
              >Varsayılan Depo{requiresHeaderWarehouse ? ' *' : ''}</Field.Label
            >
            <EntityCombobox
              id="document-warehouse"
              selected={form.defaultWarehouse}
              results={warehouses}
              title="Depo seç"
              description="Yalnızca erişebildiğiniz aktif standart depolar gösterilir."
              triggerLabel="Varsayılan depo"
              triggerPlaceholder="Depo kodu veya adı ara…"
              searchPlaceholder="Depo kodu veya adı ara…"
              disabled={editorMutationDisabled}
              onSelect={(option) => {
                form.defaultWarehouse = option;
                if (!form.branchID && option.branchID) {
                  form.branchID = option.branchID;
                  form.branchName = option.subtitle ?? 'Şube';
                }
              }}
            />
          </Field.Field>
          <Field.Field class="span-2">
            <Field.Label for="document-notes">Açıklama</Field.Label>
            <textarea
              id="document-notes"
              bind:value={form.notes}
              disabled={editorMutationDisabled}
              maxlength={1000}
              placeholder="Belge açıklaması veya sevk notu"
            ></textarea>
          </Field.Field>
          {#if (isSales && resource !== 'quotes') || (!isSales && resource !== 'orders')}
            <Field.Field class="span-2">
              <Field.Label for="source-document">Seçili kaynak belge</Field.Label>
              {#key sourcePickerKey}
                <EntityCombobox
                  id="source-document"
                  selected={form.sourceDocument}
                  title="Kaynak belge seç"
                  description="Aynı şirket, cari ve para birimindeki kullanılabilir kaynaklar gösterilir."
                  triggerLabel="Seçili kaynak belge"
                  triggerPlaceholder="Doğrudan belge"
                  searchPlaceholder="Belge no veya cari ara…"
                  initialEmptyText={sourceInitialEmptyText()}
                  emptyText={sourceEmptyText()}
                  onSearch={searchSourceDocuments}
                  minQueryLength={0}
                  clearable
                  onClear={() => {
                    if (lines.length) {
                      askConfirm({
                        title: 'Kaynağı kaldır',
                        description:
                          'Kaynak kaldırıldığında kaynak satırları temizlenecektir. Devam edilsin mi?',
                        confirmLabel: 'Kaynağı kaldır',
                        run: clearSource
                      });
                      return;
                    }
                    clearSource();
                  }}
                  disabled={editorMutationDisabled}
                  onSelect={(option) => void selectSourceAndLoad(option)}
                />
              {/key}
              {#if form.sourceDocuments.length}
                <div class="source-reference-list" aria-label="Kaynak belgeler">
                  {#each form.sourceDocuments as source}
                    {#if source.href && source.documentNo}
                      <a
                        class="source-reference-link"
                        href={source.href}
                        aria-label={`${sourceReferenceLabel(source)} kaynak belgesini aç`}
                      >
                        {sourceReferenceLabel(source)}
                      </a>
                    {:else}
                      <span class="source-reference-text">{sourceReferenceLabel(source)}</span>
                    {/if}
                  {/each}
                </div>
              {/if}
              <Field.Description
                >{sourceLoading
                  ? 'Kaynak satırları yükleniyor…'
                  : resource === 'orders'
                    ? 'Yalnızca kabul edilmiş ve kullanılabilir teklifler listelenir.'
                    : resource === 'invoices'
                      ? 'Siparişlerden yalnızca karşılanmış ürün ve faturalanabilir hizmet satırları alınır.'
                      : resource === 'dispatches'
                        ? 'Siparişlerden yalnızca karşılanmamış ürün satırları alınır.'
                        : resource === 'returns'
                          ? isSales
                            ? 'İade satırlarını doldurmak için faturalanmış veya sevk edilmiş bir kaynak belge seçin.'
                            : 'İade satırlarını doldurmak için bir alış irsaliyesi seçin.'
                          : 'Doğrudan kayıt için bu alanı boş bırakın.'}</Field.Description
              >
            </Field.Field>
          {/if}
          {#if resource === 'returns'}
            <Field.Field
              class="span-2"
              data-invalid={validationError === 'İade gerekçesi gereklidir.'}
            >
              <Field.Label for="return-reason">İade Gerekçesi <span>*</span></Field.Label>
              <Input
                id="return-reason"
                bind:value={form.reason}
                disabled={editorMutationDisabled}
                maxlength={500}
                placeholder="İade nedeni"
                required
                aria-invalid={validationError === 'İade gerekçesi gereklidir.'}
                aria-describedby={validationError === 'İade gerekçesi gereklidir.'
                  ? 'return-reason-error'
                  : undefined}
                oninput={() => {
                  if (validationError === 'İade gerekçesi gereklidir.') validationError = '';
                }}
              />
              {#if validationError === 'İade gerekçesi gereklidir.'}
                <Field.Error id="return-reason-error">İade gerekçesi gereklidir.</Field.Error>
              {/if}
            </Field.Field>
          {/if}
        </div>
      </section>

      <section class="document-card lines-card" aria-labelledby="lines-title">
        <div class="section-heading">
          <div>
            <span class="section-kicker">02 · Belge satırları</span>
            <h2 id="lines-title">Ürün / hizmet hareketleri</h2>
          </div>
          {#if isEditable}
            <div class="line-actions">
              {#if canUseService}<Button
                  size="sm"
                  variant="outline"
                  disabled={editorMutationDisabled}
                  onclick={() => addLine('SERVICE')}><Plus size={13} />Hizmet satırı</Button
                >{/if}
              <Button
                size="sm"
                variant="outline"
                disabled={editorMutationDisabled}
                onclick={() => addLine('PRODUCT')}><Plus size={13} />Ürün satırı</Button
              >
            </div>
          {/if}
        </div>
        <div class="line-table-wrap">
          <table class="line-table">
            <thead
              ><tr
                ><th class="line-number">#</th><th class="line-type-cell">Tür</th><th
                  class="product-col">Ürün / hizmet</th
                ><th>Depo</th><th>Birim</th><th class="numeric">Miktar</th><th class="numeric"
                  >Birim fiyat</th
                ><th class="numeric">Satır toplamı</th><th class="line-action-col"
                  ><span class="sr-only">İşlem</span></th
                ></tr
              ></thead
            >
            <tbody>
              {#each lines as line, index (line.id ?? index)}
                <tr class={lineError(index) ? 'has-line-error' : ''} data-line-number={index + 1}>
                  <td class="line-number" data-label="No">{index + 1}</td>
                  <td class="line-type-cell" data-label="Tür">
                    {#if isEditable}
                      <select
                        class="line-type-select"
                        value={line.lineType}
                        aria-label={`${index + 1}. satır türü`}
                        onchange={(event) =>
                          changeLineType(line, (event.currentTarget as HTMLSelectElement).value)}
                      >
                        <option value="PRODUCT">Ürün</option>
                        {#if canUseService}<option value="SERVICE">Hizmet</option>{/if}
                      </select>
                    {:else}<span class="line-type"
                        >{line.lineType === 'SERVICE' ? 'Hizmet' : 'Ürün'}</span
                      >{/if}
                  </td>
                  <td class="product-cell" data-label="Ürün / hizmet">
                    {#if isEditable}
                      <EntityCombobox
                        id={`line-${index + 1}-product`}
                        selected={line.product}
                        title={line.lineType === 'SERVICE' ? 'Hizmet kartı seç' : 'Ürün kartı seç'}
                        description="Aktif kartlardan birini seçin; fiyat ve vergi sunucu tarafından çözümlenir."
                        triggerLabel={`${index + 1}. satır kartı`}
                        triggerPlaceholder={line.lineType === 'SERVICE'
                          ? 'Serbest hizmet veya kart'
                          : 'Ürün seçin'}
                        searchPlaceholder="Kod, ad veya SKU ara…"
                        onSearch={(query, signal) => searchProducts(line.lineType, query, signal)}
                        minQueryLength={0}
                        disabled={editorMutationDisabled}
                        onSelect={(option) => selectProduct(line, option)}
                      />
                      {#if lineError(index, 'product_id')}<small class="field-error"
                          >{lineError(index, 'product_id')}</small
                        >{/if}
                    {:else}
                      <span class="snapshot-copy">
                        <strong>{line.product?.title || line.description || 'Satır'}</strong>
                        {#if line.variant}<small class="variant-snapshot"
                            >{line.variant.title}</small
                          >{/if}
                      </span>
                    {/if}
                  </td>
                  <td data-label="Depo">
                    {#if line.lineType === 'PRODUCT'}
                      {#if isEditable}
                        <EntityCombobox
                          id={`line-${index + 1}-warehouse`}
                          selected={line.warehouse}
                          results={warehouses}
                          title="Satır deposu seç"
                          description="Satır deposu varsayılan depodan bağımsız değiştirilebilir."
                          triggerLabel={`${index + 1}. satır deposu`}
                          triggerPlaceholder="Depo seçin"
                          searchPlaceholder="Depo ara…"
                          disabled={editorMutationDisabled}
                          onSelect={(option) => (line.warehouse = option)}
                        />
                        {#if lineError(index, 'warehouse_id')}<small class="field-error"
                            >{lineError(index, 'warehouse_id')}</small
                          >{/if}
                      {:else}<span class="snapshot-copy">{line.warehouse?.title || 'Depo'}</span
                        >{/if}
                    {:else}<span class="muted">—</span>{/if}
                  </td>
                  <td data-label="Birim"
                    ><Input
                      class="unit-input"
                      value={line.unitCode || 'ADET'}
                      readonly
                      aria-readonly="true"
                      aria-label={`${index + 1}. satır birimi`}
                    /></td
                  >
                  <td class="numeric" data-label="Miktar"
                    ><QuantityInput
                      id={`line-${index + 1}-quantity`}
                      ariaLabel={`${index + 1}. satır miktarı`}
                      bind:value={line.quantity}
                      disabled={editorMutationDisabled}
                      unit={line.unitCode}
                    />{#if lineError(index, 'quantity')}<small class="field-error"
                        >{lineError(index, 'quantity')}</small
                      >{/if}{#if lineProgressText(line)}<small class="line-progress"
                        >{lineProgressText(line)}</small
                      >{/if}{#if line.sourceRemainingQuantity}<small class="source-remaining"
                        >Kaynakta kalan: {formatQuantityWithUnit(
                          line.sourceRemainingQuantity,
                          line.unitCode
                        )}</small
                      >{/if}</td
                  >
                  <td class="numeric" data-label="Birim fiyat"
                    ><MoneyInput
                      id={`line-${index + 1}-unit-price`}
                      ariaLabel={`${index + 1}. satır birim fiyatı`}
                      bind:value={line.unitPrice}
                      {currency}
                      disabled={editorMutationDisabled}
                      oninput={() => (line.manualPrice = true)}
                    />{#if lineError(index, 'unit_price')}<small class="field-error"
                        >{lineError(index, 'unit_price')}</small
                      >{/if}</td
                  >
                  <td class="numeric total-cell" data-label="Satır toplamı"
                    >{lineDisplayTotal(line)}{#if lineTaxBreakdown(line).length > 0}<span
                        class="line-taxes"
                        >{#each lineTaxBreakdown(line) as component}<small
                            ><span>{component.label}</span><span>{component.amount}</span></small
                          >{/each}</span
                      >{/if}</td
                  >
                  <td class="line-action-col" data-label="İşlem">
                    {#if isEditable}<Button
                        size="icon"
                        variant="ghost"
                        title="Satırı sil"
                        aria-label={`${index + 1}. satırı sil`}
                        onclick={() => removeLine(index)}><X size={14} /></Button
                      >{/if}
                  </td>
                </tr>
                {#if isEditable}
                  <tr class="line-options-row"
                    ><td></td><td colspan="8"
                      ><div class="line-options">
                        {#if line.product?.variantsEnabled}
                          <label class="variant-option" for={`line-${index + 1}-variant`}
                            >Varyant <span>*</span>
                            <EntityPickerDialog
                              id={`line-${index + 1}-variant`}
                              selected={line.variant}
                              results={line.variants}
                              title="Varyant seç"
                              description="Bu ürünün aktif varyantlarından birini seçin."
                              triggerLabel={`${index + 1}. satır varyantı`}
                              triggerPlaceholder={line.variantLoading
                                ? 'Varyantlar yükleniyor…'
                                : 'Varyant seçin'}
                              searchPlaceholder="Varyant kodu veya özelliği ara…"
                              loading={line.variantLoading}
                              error={line.variantError}
                              disabled={editorMutationDisabled}
                              onSelect={(option) => selectVariant(line, option)}
                            /></label
                          >
                        {/if}
                        <label
                          >İskonto % <Input
                            bind:value={line.discountRate}
                            inputmode="decimal"
                            disabled={editorMutationDisabled}
                            aria-label={`${index + 1}. satır iskonto oranı`}
                          />{#if lineError(index, 'discount_rate')}<small class="field-error"
                              >{lineError(index, 'discount_rate')}</small
                            >{/if}</label
                        ><label
                          >KDV % <Input
                            bind:value={line.taxRate}
                            inputmode="decimal"
                            disabled={editorMutationDisabled}
                            aria-label={`${index + 1}. satır KDV oranı`}
                          />{#if lineError(index, 'tax_rate')}<small class="field-error"
                              >{lineError(index, 'tax_rate')}</small
                            >{/if}</label
                        >{#if line.lineType === 'PRODUCT'}<span
                            >Varsayılan depo yalnızca yeni ürün satırına uygulanır.</span
                          >{/if}
                      </div></td
                    ></tr
                  >
                {/if}
              {/each}
            </tbody>
          </table>
        </div>
      </section>

      <section class="document-card totals-card" aria-labelledby="totals-title">
        <div>
          <span class="section-kicker">03 · Belge özeti</span>
          <h2 id="totals-title">Toplamlar</h2>
        </div>
        <div class="totals-grid">
          <span>Brüt toplam</span><strong>{formatMoney(displaySubtotal, currency)}</strong>
          <span>İskonto</span><strong>{formatMoney(displayDiscountTotal, currency)}</strong>
          <span>KDV</span><strong>{formatMoney(displayVatTotal, currency)}</strong>
          {#if decimalParts(displayAdditionalTaxTotal)[0] !== 0n}<span>Ek vergiler</span><strong
              >{formatMoney(displayAdditionalTaxTotal, currency)}</strong
            ><span>Toplam vergi</span><strong>{formatMoney(displayTaxTotal, currency)}</strong>{/if}
          <span>Vergili toplam</span><strong>{formatMoney(displayGrandTotal, currency)}</strong>
          {#if !isEditable && resource === 'invoices' && record?.payable_total && record.payable_total !== serverGrandTotal}<span
              >Fatura borç toplamı</span
            ><strong>{formatMoney(text(record.payable_total), currency)}</strong>{/if}
          <span>Satır sayısı</span><strong>{formatQuantity(String(lines.length))}</strong>
        </div>
        {#if isEditable}
          <p class="totals-note">
            Tutarlar önizlemedir; kesin vergi ve toplamlar belge kaydedildiğinde sunucuda
            hesaplanır.
          </p>
        {/if}
      </section>

      {#if !isNew && resource === 'invoices' && record?.settlement}
        <section class="document-card settlement-card" aria-labelledby="settlement-title">
          <div>
            <span class="section-kicker">04 · Cari özeti</span>
            <h2 id="settlement-title">
              {isSales ? 'Tahsilat ve iade özeti' : 'Ödeme ve iade özeti'}
            </h2>
          </div>
          <div class="totals-grid">
            <span>Fatura Toplamı</span><strong
              >{formatMoney(text(record.settlement.invoice_total, '0'), currency)}</strong
            >
            <span>İade Edilen</span><strong
              >{formatMoney(text(record.settlement.returned_total, '0'), currency)}</strong
            >
            <span>{isSales ? 'Net Fatura' : 'Net Borç'}</span><strong
              >{formatMoney(text(record.settlement.effective_invoice_total, '0'), currency)}</strong
            >
            <span>{isSales ? 'Tahsil Edilen' : 'Ödenen'}</span><strong
              >{formatMoney(
                text(
                  isSales ? record.settlement.collected_total : record.settlement.paid_total,
                  '0'
                ),
                currency
              )}</strong
            >
            <span>{isSales ? 'Kalan Tahsilat' : 'Kalan Ödeme'}</span><strong
              >{formatMoney(
                text(
                  isSales ? record.settlement.amount_due : record.settlement.amount_payable,
                  '0'
                ),
                currency
              )}</strong
            >
            {#if decimalParts(text(isSales ? record.settlement.customer_credit : record.settlement.supplier_credit, '0'))[0] > 0n}
              <span>{isSales ? 'Müşteri Alacağı' : 'Tedarikçiden Alacak'}</span><strong
                >{formatMoney(
                  text(
                    isSales ? record.settlement.customer_credit : record.settlement.supplier_credit,
                    '0'
                  ),
                  currency
                )}</strong
              >
            {/if}
          </div>
        </section>
      {/if}

      {#if !isNew && documentStatus !== 'DRAFT'}
        <section class="document-card related-card" aria-labelledby="related-title">
          <div>
            <span class="section-kicker">05 · Belge geçmişi</span>
            <h2 id="related-title">İlişkili kayıtlar</h2>
          </div>
          {#if form.sourceDocuments.length}
            <div class="related-reference-list" aria-label="Kaynak belgeler">
              <strong>Kaynak belgeler:</strong>
              {#each form.sourceDocuments as source}
                {#if source.href && source.documentNo}
                  <a
                    class="source-reference-link"
                    href={source.href}
                    aria-label={`${sourceReferenceLabel(source)} kaynak belgesini aç`}
                  >
                    {sourceReferenceLabel(source)}
                  </a>
                {:else}
                  <span class="source-reference-text">{sourceReferenceLabel(source)}</span>
                {/if}
              {/each}
            </div>
          {/if}
          {#if record?.related_documents?.length}
            <div class="related-reference-list" aria-label="İlişkili belgeler">
              <strong>İlişkili belgeler:</strong>
              {#each record.related_documents as related}
                {@const relatedOption = sourceOptionFromReference(related)}
                {#if relatedOption}
                  {#if relatedOption.href && relatedOption.documentNo}
                    <a
                      class="source-reference-link"
                      href={relatedOption.href}
                      aria-label={`${sourceReferenceLabel(relatedOption)} belgesini aç`}
                    >
                      {sourceReferenceLabel(relatedOption)}
                    </a>
                  {:else}
                    <span class="source-reference-text">{sourceReferenceLabel(relatedOption)}</span>
                  {/if}
                {/if}
              {/each}
            </div>
          {/if}
          <div class="related-status-grid">
            <span>Belge durumu</span><strong
              >{axisLabel('lifecycle_status', record?.lifecycle_status ?? documentStatus)}</strong
            >
            {#if record?.fulfillment_status}<span>Karşılama</span><strong
                >{axisLabel('fulfillment_status', record.fulfillment_status)}</strong
              >{/if}
            {#if record?.fulfillment_at}<span>Karşılama zamanı</span><strong
                >{formatDate(record.fulfillment_at, true)}</strong
              >{/if}
            {#if record?.invoicing_status}<span>Faturalama</span><strong
                >{axisLabel('invoicing_status', record.invoicing_status)}</strong
              >{/if}
            {#if record?.payment_status}<span>Ödeme</span><strong
                >{axisLabel('payment_status', record.payment_status)}</strong
              >{/if}
          </div>
          {#if invoiceStockEffectText()}<p>{invoiceStockEffectText()}</p>{/if}
          <p class="read-only-note">
            Cari hareketi ve stok hareketi ilgili kayıt detayından izlenebilir. Sonlandırılmış belge
            üzerinde miktar veya toplam değiştirilemez.
          </p>
        </section>
      {/if}
    </div>
  {/if}
  <ConfirmDialog
    bind:open={unsavedNavOpen}
    title="Kaydedilmemiş değişiklikler"
    description="Bu belgede kaydedilmemiş değişiklikler var. Sayfadan ayrılırsanız bu değişiklikler kaybolur."
    confirmLabel="Yine de ayrıl"
    cancelLabel="Sayfada kal"
    onConfirm={confirmLeaveUnsaved}
  />
  <ConfirmDialog
    bind:open={deleteConfirmOpen}
    title="Taslağı sil"
    description="Bu taslak belge kalıcı olarak silinecek. Devam etmek istiyor musunuz?"
    confirmLabel="Taslağı sil"
    onConfirm={deleteDraft}
  />
  <ReasonDialog
    bind:open={cancelDialogOpen}
    title="Belgeyi iptal et"
    description={cancelEffectPreview}
    label="İptal gerekçesi"
    placeholder="İptal nedenini yazın…"
    confirmLabel="Belgeyi iptal et"
    onConfirm={async (reason) => {
      error = '';
      await runCommand('cancel', reason);
      if (error) throw new Error(error);
    }}
  />
  {#if genericConfirm}
    <ConfirmDialog
      bind:open={genericConfirmOpen}
      title={genericConfirm.title}
      description={genericConfirm.description}
      confirmLabel={genericConfirm.confirmLabel}
      onConfirm={() => genericConfirm?.run()}
    />
  {/if}
{:else}
  <StateBlock error="Belge ekranı bulunamadı." />
{/if}

<style>
  .document-layout {
    display: grid;
    gap: 10px;
    max-width: 1480px;
  }
  .document-card {
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    background: var(--surface);
    padding: 14px;
    box-shadow: var(--shadow-subtle);
  }
  .section-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 12px;
  }
  .section-kicker {
    color: var(--primary);
    font-size: 10px;
    font-weight: 800;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  h2 {
    margin: 3px 0 0;
    font-size: 15px;
    letter-spacing: -0.01em;
  }
  .form-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 10px;
  }
  .form-grid :global([data-slot='field']) {
    min-width: 0;
  }
  .form-grid :global([data-slot='field-label']) {
    font-size: 11px;
    font-weight: 700;
  }
  .form-grid :global([data-slot='field-label'] span) {
    color: var(--danger);
  }
  .span-2 {
    grid-column: span 2;
  }
  select,
  textarea {
    min-height: var(--control-height);
    width: 100%;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 6px 9px;
    font-size: 13px;
    outline: none;
  }
  select {
    min-width: 0;
    max-width: 100%;
  }
  select:focus-visible,
  textarea:focus-visible {
    border-color: var(--primary);
    box-shadow: 0 0 0 2px var(--focus);
  }
  textarea {
    min-height: 66px;
    resize: vertical;
  }
  .line-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .line-table-wrap {
    overflow-x: auto;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
  }
  .line-table {
    width: 100%;
    min-width: 1050px;
    border-collapse: collapse;
    font-size: 12px;
  }
  .line-progress {
    display: block;
    margin-top: 4px;
    color: var(--text-muted);
    font-size: 10px;
    line-height: 1.35;
    white-space: nowrap;
  }
  .source-remaining {
    display: block;
    margin-top: 3px;
    color: var(--primary);
    font-size: 10px;
    line-height: 1.35;
    white-space: nowrap;
  }
  .status-stack {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 4px;
  }
  .fulfillment-time {
    margin: 5px 0 0;
    color: var(--text-muted);
    font-size: 11px;
    text-align: right;
  }
  .related-status-grid {
    display: grid;
    grid-template-columns: minmax(120px, 180px) minmax(0, 1fr);
    gap: 5px 12px;
    margin: 12px 0;
    max-width: 520px;
    font-size: 12px;
  }
  .related-status-grid span {
    color: var(--text-muted);
  }
  .source-reference-list,
  .related-reference-list {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
    margin-top: 8px;
    font-size: 12px;
  }
  .source-reference-list {
    color: var(--text-muted);
  }
  .source-reference-link {
    color: var(--primary);
    font-weight: 750;
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  .source-reference-link:focus-visible,
  .error-link:focus-visible {
    border-radius: 3px;
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  .source-reference-text {
    color: var(--text-muted);
  }
  .has-line-error td {
    background: color-mix(in srgb, var(--danger) 5%, var(--surface));
  }
  .field-error {
    display: block;
    color: var(--danger);
    font-size: 10px;
    line-height: 1.35;
  }
  .line-table th {
    height: 34px;
    padding: 6px 8px;
    border-bottom: 1px solid var(--border);
    background: var(--surface-muted);
    color: var(--text-muted);
    text-align: left;
    font-size: 10px;
    font-weight: 800;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  .line-table td {
    padding: 7px 8px;
    border-bottom: 1px solid var(--border-subtle, var(--border));
    vertical-align: top;
  }
  .line-table tbody tr:last-child td {
    border-bottom: 0;
  }
  .line-number {
    width: 35px;
    color: var(--text-muted);
    text-align: center;
  }
  .line-table td.line-number {
    vertical-align: middle;
  }
  .line-type-cell {
    min-width: 104px;
    white-space: nowrap;
  }
  .line-type-select {
    width: 100%;
    min-width: 96px;
    max-width: 100%;
  }
  .product-col {
    min-width: 240px;
  }
  .product-cell {
    min-width: 240px;
  }
  /* Keep this a real table-cell (no display:grid/flex): a non-table-cell
     display stops the cell from stretching to the row height, so its
     border-bottom is drawn short and the divider looks misaligned. */
  .product-cell > * + * {
    margin-top: 6px;
  }
  .variant-option :global(.entity-picker-trigger) {
    min-width: 200px;
    min-height: 30px;
    padding-block: 3px;
  }
  .variant-option {
    font-weight: 750;
  }
  .variant-option span {
    color: var(--danger);
  }
  .variant-snapshot {
    display: block;
    margin-top: 2px;
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 500;
  }
  .line-table :global(.entity-picker-trigger),
  .line-table :global(.entity-combobox) {
    min-width: 210px;
  }
  /* Keep the picker trigger a single, tidy line -- the full name is shown in
     the dialog. Without this a long product name breaks mid-word and the
     button grows unevenly, which is especially rough in the mobile card view. */
  .line-table :global(.entity-picker-trigger .trigger-copy strong),
  .line-table :global(.entity-picker-trigger .placeholder) {
    overflow: hidden;
    overflow-wrap: normal;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .line-table :global(.unit-input) {
    min-width: 96px;
    max-width: 100%;
    text-overflow: ellipsis;
  }
  .numeric {
    text-align: right;
    white-space: nowrap;
  }
  .numeric :global(.quantity-input),
  .numeric :global(.money-input) {
    min-width: 110px;
  }
  .total-cell {
    font-weight: 750;
  }
  .line-table td.total-cell {
    vertical-align: middle;
  }
  .line-action-col {
    width: 44px;
    text-align: center;
  }
  .line-table td.line-action-col {
    vertical-align: middle;
  }
  .line-type,
  .snapshot-copy {
    display: flex;
    flex-direction: column;
    justify-content: center;
    min-height: var(--control-height);
    font-weight: 650;
  }
  .line-type {
    white-space: nowrap;
  }
  .line-table td .muted {
    display: flex;
    align-items: center;
    min-height: var(--control-height);
  }
  .line-options-row td {
    padding-top: 0;
    background: color-mix(in srgb, var(--surface-muted) 65%, var(--surface));
  }
  .line-options {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .line-options label {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .line-options :global(input) {
    width: 84px;
    height: 28px;
    font-size: 12px;
  }
  .muted,
  .read-only-note,
  .related-card p {
    color: var(--text-muted);
  }
  .line-taxes {
    display: grid;
    gap: 2px;
    margin-top: 4px;
  }
  .line-taxes small {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }

  .totals-card {
    display: flex;
    justify-content: space-between;
    gap: 24px;
    align-items: flex-start;
  }
  .totals-grid {
    min-width: min(360px, 100%);
    display: grid;
    grid-template-columns: 1fr auto;
    gap: 7px 24px;
    font-size: 12px;
  }
  .totals-grid span {
    color: var(--text-muted);
  }
  .totals-grid strong {
    text-align: right;
  }
  .totals-note {
    margin: 10px 0 0;
    font-size: 0.78rem;
    color: var(--text-muted, var(--muted-foreground));
  }
  .status-pill {
    display: inline-flex;
    min-height: 22px;
    align-items: center;
    padding: 3px 8px;
    border: 1px solid var(--border);
    border-radius: 999px;
    font-size: 11px;
    font-weight: 750;
  }
  .status-pill.success {
    color: var(--success);
    background: color-mix(in srgb, var(--success) 10%, var(--surface));
    border-color: color-mix(in srgb, var(--success) 30%, var(--border));
  }
  .status-pill.info {
    color: var(--info);
    background: color-mix(in srgb, var(--info) 10%, var(--surface));
    border-color: color-mix(in srgb, var(--info) 30%, var(--border));
  }
  .status-pill.warning {
    color: var(--warning);
    background: color-mix(in srgb, var(--warning) 10%, var(--surface));
    border-color: color-mix(in srgb, var(--warning) 30%, var(--border));
  }
  .status-pill.danger {
    color: var(--danger);
    background: color-mix(in srgb, var(--danger) 10%, var(--surface));
    border-color: color-mix(in srgb, var(--danger) 30%, var(--border));
  }
  .read-only-note {
    align-self: center;
    font-size: 11px;
  }
  .cancel-blocked-note {
    max-width: 340px;
    line-height: 1.4;
  }
  .cancel-blocked-links a {
    color: var(--danger);
    text-decoration: underline;
  }
  .permission-card,
  .form-error {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 12px 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    background: var(--surface);
    font-size: 12px;
  }
  .permission-card {
    color: var(--danger);
  }
  .form-error {
    color: var(--danger);
    border-color: color-mix(in srgb, var(--danger) 35%, var(--border));
    margin-bottom: 10px;
  }
  .error-summary ul {
    margin: 0;
    padding-left: 18px;
  }
  .error-link {
    border: 0;
    background: transparent;
    color: inherit;
    cursor: pointer;
    padding: 1px 0;
    text-align: left;
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
  @media (max-width: 900px) {
    .document-card {
      padding: 12px;
    }
    .line-table-wrap {
      overflow: visible;
      border: 0;
    }
    .line-table {
      display: block;
      min-width: 0;
    }
    .line-table thead {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip: rect(0 0 0 0);
      white-space: nowrap;
    }
    .line-table tbody {
      display: grid;
      gap: 10px;
    }
    .line-table tr:not(.line-options-row) {
      position: relative;
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 10px 12px;
      border: 1px solid var(--border);
      border-radius: var(--radius-control);
      background: var(--surface);
      padding: 50px 10px 10px;
    }
    .line-table tr:not(.line-options-row) td {
      display: block;
      width: 100%;
      min-width: 0;
      padding: 0;
      border: 0;
      text-align: left;
    }
    /* Captions only on the fields where they add clarity. */
    .line-table tr:not(.line-options-row) td[data-label='Miktar']::before,
    .line-table tr:not(.line-options-row) td[data-label='Birim fiyat']::before,
    .line-table tr:not(.line-options-row) td[data-label='Depo']::before,
    .line-table tr:not(.line-options-row) td[data-label='Satır toplamı']::before {
      content: attr(data-label);
      display: block;
      margin-bottom: 4px;
      color: var(--text-muted);
      font-size: 10px;
      font-weight: 800;
      letter-spacing: 0.03em;
      text-transform: uppercase;
    }
    /* Full-width rows. */
    .line-table tr:not(.line-options-row) .product-cell,
    .line-table tr:not(.line-options-row) td[data-label='Depo'],
    .line-table tr:not(.line-options-row) .total-cell {
      grid-column: 1 / -1;
    }
    /* The unit is already shown inside the quantity input. */
    .line-table tr:not(.line-options-row) td[data-label='Birim'] {
      display: none;
    }
    /* Hide the empty warehouse cell on service lines. */
    .line-table tr:not(.line-options-row) td[data-label='Depo']:has(.muted) {
      display: none;
    }
    /* Line number: a small tag in the card's top-left corner. */
    .line-table tr:not(.line-options-row) .line-number {
      position: absolute;
      top: 8px;
      left: 10px;
      width: auto;
      padding: 1px 7px;
      border-radius: 999px;
      background: var(--surface-muted);
      color: var(--text-muted);
      font-size: 11px;
      font-weight: 700;
    }
    .line-table tr:not(.line-options-row) .line-type-cell {
      position: absolute;
      top: 8px;
      left: 46px;
      right: 44px;
      width: auto;
    }
    .line-table tr:not(.line-options-row) .line-type-cell .line-type-select {
      min-height: 34px;
      height: 34px;
      padding-block: 0;
    }
    .line-table tr:not(.line-options-row) .line-type-cell .line-type {
      display: inline-block;
      padding: 6px 0;
    }
    .line-table tr:not(.line-options-row) .line-action-col {
      position: absolute;
      top: 6px;
      right: 6px;
      display: block;
      width: auto;
      padding: 0;
      border: 0;
    }
    .line-table tr:not(.line-options-row) .line-number::before,
    .line-table tr:not(.line-options-row) .line-type-cell::before,
    .line-table tr:not(.line-options-row) .line-action-col::before {
      display: none;
    }
    .line-table tr:not(.line-options-row) .total-cell {
      margin-top: 2px;
      padding-top: 8px;
      border-top: 1px solid var(--border-subtle, var(--border));
      text-align: right;
      font-size: 13px;
      font-weight: 750;
    }
    .line-table :global(.entity-picker-trigger),
    .line-table :global(.entity-combobox),
    .product-cell,
    .line-table :global(.unit-input),
    .line-type-select {
      width: 100%;
      min-width: 0;
      max-width: 100%;
    }
    .line-table select,
    .line-table :global(input),
    .line-table :global(.entity-picker-trigger),
    .line-table :global(button) {
      min-height: 44px;
      min-width: 0;
      max-width: 100%;
    }
    .line-table :global(.entity-picker-trigger) {
      padding-inline: 8px;
    }
    .line-table :global(.entity-combobox .combobox-input) {
      min-height: 44px;
    }
    .line-table .numeric {
      white-space: normal;
      text-align: left;
    }
    .numeric :global(.quantity-input),
    .numeric :global(.money-input) {
      width: 100%;
      min-width: 0;
    }
    .line-options-row {
      display: block;
      border: 1px solid var(--border);
      border-top: 0;
      border-radius: 0 0 var(--radius-control) var(--radius-control);
      margin-top: -11px;
      padding: 8px 10px 10px;
      background: var(--surface-muted);
    }
    .line-options-row td:first-child {
      display: none;
    }
    .line-options-row td:last-child {
      display: block;
      padding: 0;
      border-bottom: 0;
    }
    .line-options {
      display: grid;
      grid-template-columns: 1fr 1fr;
      align-items: start;
      gap: 8px 10px;
    }
    .line-options label {
      display: grid;
      gap: 4px;
      min-height: 0;
    }
    .line-options > span {
      grid-column: 1 / -1;
      font-size: 10px;
      line-height: 1.35;
    }
    .line-options :global(input) {
      width: 100%;
      min-height: 40px;
    }
    .line-options .variant-option {
      grid-column: 1 / -1;
    }
    .line-options .variant-option :global(.entity-picker-trigger) {
      width: 100%;
      min-width: 0;
    }
  }
  @media (max-width: 430px) {
    .document-card {
      padding: 10px;
    }
    .line-table :global(.entity-picker-trigger) {
      font-size: 12px;
    }
  }
  @media (max-width: 900px) {
    .form-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
    .span-2 {
      grid-column: span 2;
    }
    .line-taxes {
      display: grid;
      gap: 2px;
      margin-top: 4px;
    }

    .line-taxes small {
      display: flex;
      justify-content: flex-end;
      gap: 8px;
      color: var(--text-muted);
      font-variant-numeric: tabular-nums;
    }

    .totals-card {
      flex-direction: column;
    }
    .totals-grid {
      width: 100%;
    }
  }
  @media (max-width: 560px) {
    .form-grid {
      grid-template-columns: 1fr;
    }
    .span-2 {
      grid-column: span 1;
    }
    .section-heading {
      flex-direction: column;
    }
  }
  @media print {
    :global(.skip-link),
    :global(.sidebar),
    :global(.topbar) {
      display: none !important;
    }
    :global(.app-shell) {
      display: block;
      min-height: auto;
    }
    :global(.workspace),
    :global(.main) {
      width: auto;
      max-width: none;
      margin: 0;
      padding: 0;
    }
    :global(.document-tools) {
      display: none !important;
    }
    :global(.header-actions .primary) {
      display: none !important;
    }
    :global(.header-actions .toolbar-status) {
      display: none !important;
    }
    .form-error,
    .reference-error,
    button {
      display: none !important;
    }
    .totals-card,
    .form-grid,
    table {
      break-inside: avoid;
    }
  }
</style>

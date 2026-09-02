<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { APIRequestError } from '$lib/api';
  import {
    CheckCircle2,
    CircleAlert,
    Download,
    FileSpreadsheet,
    FileUp,
    LoaderCircle,
    Table2,
    X
  } from '@lucide/svelte';
  import { Badge } from '$lib/components/ui/badge';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Empty from '$lib/components/ui/empty';
  import * as Field from '$lib/components/ui/field';
  import * as Alert from '$lib/components/ui/alert';
  import * as Tabs from '$lib/components/ui/tabs';
  import { formatMoney, formatQuantity } from '$lib/design/formatters';
  import {
    analyzeImport,
    buildImportMapping,
    commitImport,
    createExport,
    getImportCapabilities,
    getImportStatus,
    isUploadSizeAllowed,
    selectableImportEntities,
    type ImportCapabilities,
    type ImportCapabilityEntity,
    type ImportCapabilityField,
    type ImportAnalysis,
    type ImportEntity,
    uploadImport
  } from '$lib/features/dataexchange/api';

  type TransferMode = 'import' | 'export';
  type DataType = Exclude<ImportEntity, 'STOCK_COUNT'>;
  type ReviewTab = 'mapping' | 'preview' | 'errors';
  type OperationStatus = 'idle' | 'processing' | 'success';
  type MappingRow = {
    id: string;
    label: string;
    type: string;
    sourceColumn: string;
    required: boolean;
    example: string;
  };
  type PreviewRow = {
    rowNumber: number;
    values: string[];
    valid: boolean;
    note: string;
  };
  const dataTypeFilenameStems: Partial<Record<DataType, string>> = {
    PRODUCT: 'urunler',
    VARIANT: 'varyantlar',
    BARCODE: 'barkodlar',
    WAREHOUSE: 'depolar',
    PARTY: 'cariler',
    PRICE_LIST: 'fiyat-listeleri',
    OPENING_STOCK: 'acilis-stoku'
  };

  const FALLBACK_MAX_UPLOAD_BYTES = 64 * 1024 * 1024;
  const ERROR_PAGE_SIZE = 10;
  let mode = $state<TransferMode>('import');
  let dataType = $state<DataType | ''>('');
  let reviewTab = $state<ReviewTab>('mapping');
  let mappingRows = $state<MappingRow[]>([]);
  let fileName = $state('');
  let fileMeta = $state('');
  let selectedFile = $state<File | null>(null);
  let analysis = $state<ImportAnalysis | null>(null);
  let importID = $state('');
  let exportFormat = $state<'csv' | 'xlsx'>('csv');
  let isProcessing = $state(false);
  let operationStatus = $state<OperationStatus>('idle');
  let operationMessage = $state('');
  let formErrors = $state<Record<string, string>>({});
  let fileInput = $state<HTMLInputElement | null>(null);
  let errorSummary = $state<HTMLDivElement | null>(null);
  let capabilities = $state<ImportCapabilities | null>(null);
  let capabilityState = $state<'loading' | 'ready' | 'error'>('loading');
  let capabilityError = $state('');
  let workflowRevision = $state(0);
  let analysisRevision = $state('');
  let commitAuthorizationKey = $state('');
  let commitIdempotencyKey = $state('');
  let uncertainCommitID = $state('');
  let errorPage = $state(1);

  function entitiesFor(direction: TransferMode, source = capabilities) {
    if (!source) return [] as ImportCapabilityEntity[];
    return selectableImportEntities(source, direction === 'import' ? 'IMPORT' : 'EXPORT').filter(
      (entity) => direction !== 'import' || entity.type !== 'BARCODE'
    );
  }

  function mappingRowsFor(entity?: ImportCapabilityEntity) {
    return (entity?.fields ?? []).map((field: ImportCapabilityField): MappingRow => ({
      id: field.name,
      label: field.label,
      type: field.type,
      sourceColumn: '',
      required: field.required,
      example: field.example
    }));
  }

  function workflowKeyFor(rows: MappingRow[]) {
    return [
      mode,
      dataType,
      selectedFile?.name ?? '',
      selectedFile?.size ?? '',
      selectedFile?.lastModified ?? '',
      rows.map((row) => `${row.id}:${row.sourceColumn.trim()}`).join('|')
    ].join('::');
  }

  const selectableEntities = $derived(entitiesFor(mode));
  const currentEntity = $derived(capabilities?.entities.find((entity) => entity.type === dataType));
  const currentEntityLabel = $derived(currentEntity?.label ?? 'Veri türü');
  const maxUploadBytes = $derived(
    capabilityState === 'loading'
      ? FALLBACK_MAX_UPLOAD_BYTES
      : Math.max(0, capabilities?.max_upload_bytes ?? 0)
  );
  const canImportCurrent = $derived(
    capabilityState === 'ready' && Boolean(currentEntity?.importable && dataType !== 'BARCODE')
  );
  const canExportCurrent = $derived(
    capabilityState === 'ready' && Boolean(currentEntity?.exportable)
  );
  const canImportAny = $derived(capabilityState === 'ready' && entitiesFor('import').length > 0);
  const canExportAny = $derived(capabilityState === 'ready' && entitiesFor('export').length > 0);
  const canUseCurrent = $derived(mode === 'import' ? canImportCurrent : canExportCurrent);
  const canStartOperation = $derived(
    !isProcessing &&
      capabilityState === 'ready' &&
      canUseCurrent &&
      (mode === 'export' || Boolean(selectedFile))
  );

  const formErrorEntries = $derived(
    Object.entries(formErrors).filter(([, message]) => Boolean(message))
  );
  const mappedCount = $derived(
    mappingRows.filter((row) => Boolean(row.sourceColumn.trim())).length
  );
  const previewFields = $derived(
    mappingRows.filter((field, index) => index < 4 || field.type.toLowerCase().includes('boolean'))
  );
  const hasReviewSource = $derived(Boolean(selectedFile || analysis));
  const currentWorkflowKey = $derived(workflowKeyFor(mappingRows));
  const canCommitAnalysis = $derived(
    Boolean(
      mode === 'import' &&
      importID &&
      analysis?.preview.can_commit &&
      commitAuthorizationKey === currentWorkflowKey &&
      analysisRevision === (analysis?.analysis_revision ?? analysis?.job.analysis_revision ?? '') &&
      !uncertainCommitID
    )
  );
  const validationSummary = $derived(
    analysis
      ? {
          total: analysis.preview.total_rows,
          valid: analysis.preview.valid_rows,
          errors: analysis.preview.invalid_rows,
          warnings: analysis.preview.warning_rows
        }
      : undefined
  );
  const previewRows = $derived<PreviewRow[]>(
    (analysis?.preview.rows ?? []).slice(0, 20).map((row) => ({
      rowNumber: row.row_number,
      values: previewFields.map((field) => formatPreviewValue(field, row.values[field.id])),
      valid: row.status === 'VALID',
      note: row.issues?.[0]?.message ?? (row.status === 'VALID' ? 'Hazır' : 'Kontrol gerekli')
    }))
  );

  const errorRows = $derived(
    (analysis?.preview.rows ?? []).filter((row) => row.status !== 'VALID')
  );
  const errorPageCount = $derived(Math.max(1, Math.ceil(errorRows.length / ERROR_PAGE_SIZE)));
  const paginatedErrorRows = $derived(
    errorRows.slice((errorPage - 1) * ERROR_PAGE_SIZE, errorPage * ERROR_PAGE_SIZE)
  );

  function formatBooleanPreviewValue(value: string) {
    switch (value.trim().toUpperCase()) {
      case 'TRUE':
      case '1':
      case 'EVET':
      case 'YES':
      case 'AKTIF':
      case 'AKTİF':
      case 'ACTIVE':
        return 'Evet';
      case 'FALSE':
      case '0':
      case 'HAYIR':
      case 'NO':
      case 'PASIF':
      case 'PASİF':
      case 'INACTIVE':
        return 'Hayır';
      default:
        return value;
    }
  }

  function formatPreviewValue(field: MappingRow, value?: string) {
    if (!value) return '—';
    const normalizedType = field.type.toLowerCase();
    if (
      normalizedType.includes('boolean') ||
      field.id === 'is_active' ||
      field.id === 'is_primary'
    ) {
      return formatBooleanPreviewValue(value);
    }
    if (field.id === 'vat_rate') return `${formatQuantity(value)}%`;
    if (
      normalizedType.includes('money') ||
      normalizedType.includes('currency') ||
      /price|amount|cost/.test(field.id)
    ) {
      return formatMoney(value, 'TRY');
    }
    if (normalizedType.includes('quantity') || /quantity|count/.test(field.id)) {
      return formatQuantity(value);
    }
    return value;
  }

  function createIdempotencyKey() {
    return (
      globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
    );
  }

  function invalidateAnalysis() {
    workflowRevision += 1;
    analysis = null;
    importID = '';
    analysisRevision = '';
    commitAuthorizationKey = '';
    commitIdempotencyKey = '';
    uncertainCommitID = '';
    errorPage = 1;
  }

  function updateDataType(next: DataType) {
    dataType = next;
    mappingRows = mappingRowsFor(capabilities?.entities.find((entity) => entity.type === next));
    invalidateAnalysis();
    formErrors = {};
    operationStatus = 'idle';
    operationMessage = '';
    reviewTab = 'mapping';
  }

  function updateMode(next: TransferMode) {
    if (mode === next) return;

    mode = next;
    const nextEntity = entitiesFor(next)[0];
    if (nextEntity) {
      dataType = nextEntity.type as DataType;
      mappingRows = mappingRowsFor(nextEntity);
    } else {
      dataType = '';
      mappingRows = [];
    }
    invalidateAnalysis();
    formErrors = {};
    operationStatus = 'idle';
    operationMessage = '';
    reviewTab = 'mapping';
  }

  function updateMapping(id: string, sourceColumn: string) {
    mappingRows = mappingRows.map((row) => (row.id === id ? { ...row, sourceColumn } : row));
    invalidateAnalysis();
    formErrors = { ...formErrors, mapping: '', operation: '' };
  }

  function chooseFile() {
    fileInput?.click();
  }

  function formatFileSize(bytes: number) {
    if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  function resetSelectedFile() {
    fileName = '';
    fileMeta = '';
    selectedFile = null;
    invalidateAnalysis();
    operationStatus = 'idle';
    operationMessage = '';
    reviewTab = 'mapping';
  }

  function handleFileChange(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;

    const extension = file.name.split('.').pop()?.toLowerCase();
    const supported = extension === 'csv' || extension === 'xlsx';
    if (!supported) {
      formErrors = {
        ...formErrors,
        'file-upload': 'CSV veya XLSX uzantılı bir dosya seçin.'
      };
      resetSelectedFile();
      operationStatus = 'idle';
      operationMessage = '';
      input.value = '';
      return;
    }
    if (!isUploadSizeAllowed(file.size, maxUploadBytes)) {
      formErrors = {
        ...formErrors,
        'file-upload':
          maxUploadBytes > 0
            ? `Dosya ${formatFileSize(maxUploadBytes)} sınırını aşmamalı.`
            : 'Dosya boyutu sınırı alınamadı. Sayfayı yenileyin.'
      };
      resetSelectedFile();
      operationStatus = 'idle';
      operationMessage = '';
      input.value = '';
      return;
    }

    formErrors = { ...formErrors, 'file-upload': '' };
    fileName = file.name;
    selectedFile = file;
    invalidateAnalysis();
    fileMeta = `${formatFileSize(file.size)} · Kontrole hazır`;
    operationStatus = 'idle';
    operationMessage = '';
    reviewTab = 'mapping';
  }

  function clearFile() {
    resetSelectedFile();
    formErrors = { ...formErrors, 'file-upload': '' };
    operationStatus = 'idle';
    operationMessage = '';
    reviewTab = 'mapping';
    if (fileInput) fileInput.value = '';
  }

  async function focusErrorSummary() {
    await tick();
    errorSummary?.focus();
  }

  async function openReviewSection(nextTab: ReviewTab, targetId: string) {
    reviewTab = nextTab;
    await tick();
    document.getElementById(targetId)?.scrollIntoView({ block: 'nearest' });
  }

  async function focusIssue(field?: string) {
    if (field) {
      reviewTab = 'mapping';
      await tick();
      document.getElementById(`mapping-${field}`)?.focus();
      return;
    }
    await openReviewSection('preview', 'preview-table');
  }

  function apiErrorMessage(cause: unknown) {
    if (cause instanceof APIRequestError) {
      if (cause.code === 'CSRF_REJECTED') {
        return 'Güvenlik doğrulaması başarısız. Sayfayı yenileyip tekrar deneyin.';
      }
      if (cause.code === 'FORBIDDEN') {
        return mode === 'import'
          ? 'Seçilen veri türünü içe aktarmak için yazma yetkiniz yok.'
          : 'Seçilen veri türünü dışa aktarmak için görüntüleme yetkiniz yok.';
      }
      return cause.message;
    }
    if (cause instanceof Error && cause.message) return cause.message;
    return 'Aktarım tamamlanamadı.';
  }

  function isUncertainCommitError(cause: unknown) {
    if (!(cause instanceof APIRequestError)) return true;
    return cause.status === 408 || cause.status >= 500 || cause.code === 'TIMEOUT';
  }

  function isCommittedJob(job: { status?: string; state?: string }) {
    return ['COMMITTED', 'COMPLETED', 'DONE', 'SUCCESS'].includes(
      String(job.status ?? job.state ?? '').toUpperCase()
    );
  }

  function isRetryableCommitJob(job: { status?: string; state?: string }) {
    return ['READY', 'READY_TO_COMMIT', 'PREVIEWED', 'VALIDATED'].includes(
      String(job.status ?? job.state ?? '').toUpperCase()
    );
  }

  function finishCommitted(rowCount: number) {
    operationStatus = 'success';
    operationMessage = `Aktarım tamamlandı. ${rowCount} satır işlendi.`;
    formErrors = { ...formErrors, operation: '' };
    invalidateAnalysis();
  }

  async function resolveCommitUncertainty(id: string, rowCount: number) {
    try {
      const job = await getImportStatus(id);
      if (isCommittedJob(job)) {
        finishCommitted(rowCount);
        return true;
      }
      if (isRetryableCommitJob(job)) {
        uncertainCommitID = '';
        operationStatus = 'idle';
        operationMessage = 'Aktarım tamamlanmadı. Durum kontrol edildi; tekrar gönderebilirsiniz.';
        formErrors = { ...formErrors, operation: '' };
        return false;
      }
      operationStatus = 'idle';
      operationMessage = '';
      formErrors = {
        ...formErrors,
        operation:
          'Aktarımın sonucu henüz kesinleşmedi. Yeniden göndermeden önce durumu tekrar kontrol edin.'
      };
      return false;
    } catch {
      operationStatus = 'idle';
      operationMessage = '';
      formErrors = {
        ...formErrors,
        operation: 'Aktarımın sonucu doğrulanamadı. Yeniden göndermeden önce durumu kontrol edin.'
      };
      return false;
    }
  }

  async function loadCapabilities() {
    try {
      const nextCapabilities = await getImportCapabilities();
      capabilities = nextCapabilities;
      capabilityState = 'ready';
      const importEntities = entitiesFor('import', nextCapabilities);
      const initialEntity = importEntities[0] ?? entitiesFor('export', nextCapabilities)[0];
      if (!importEntities.length && initialEntity) mode = 'export';
      if (initialEntity) {
        dataType = initialEntity.type as DataType;
        mappingRows = mappingRowsFor(initialEntity);
      }
    } catch (cause) {
      capabilityState = 'error';
      capabilityError = apiErrorMessage(cause);
    }
  }

  async function startOperation(event: SubmitEvent) {
    event.preventDefault();
    const errors: Record<string, string> = {};
    if (!dataType || !canUseCurrent) {
      errors.operation =
        mode === 'export'
          ? 'Seçilen veri türünü dışa aktarmak için görüntüleme yetkiniz yok.'
          : 'Seçilen veri türünü içe aktarmak için gerekli yetkiniz yok.';
    }
    if (mode === 'import' && !selectedFile) {
      errors['file-upload'] = 'İşleme başlamadan önce bir dosya seçin.';
    }
    if (Object.keys(errors).length) {
      formErrors = errors;
      reviewTab = 'errors';
      await focusErrorSummary();
      return;
    }

    isProcessing = true;
    operationStatus = 'processing';
    operationMessage = '';
    formErrors = { ...formErrors, operation: '' };
    try {
      if (mode === 'export') {
        const exportJob = await createExport(
          dataType as DataType,
          '',
          exportFormat.toUpperCase() as 'CSV' | 'XLSX'
        );
        const exportedFileName =
          exportJob.filename ??
          `${dataTypeFilenameStems[dataType as DataType] ?? dataType.toLowerCase()}.${exportFormat}`;
        operationStatus = 'success';
        operationMessage = `${exportedFileName} indiriliyor.`;
        formErrors = { ...formErrors, operation: '' };
        window.location.assign(`/api/v1/exports/${encodeURIComponent(exportJob.id)}/download`);
        return;
      }

      if (uncertainCommitID && uncertainCommitID === importID) {
        await resolveCommitUncertainty(importID, analysis?.preview.total_rows ?? 0);
        return;
      }

      if (canCommitAnalysis && importID && analysis) {
        const completedImportID = importID;
        const rowCount = analysis.preview.total_rows;
        const idempotencyKey = commitIdempotencyKey || createIdempotencyKey();
        commitIdempotencyKey = idempotencyKey;
        try {
          await commitImport(completedImportID, false, idempotencyKey, analysisRevision);
          finishCommitted(rowCount);
        } catch (cause) {
          if (cause instanceof APIRequestError && cause.code === 'IMPORT_COMMIT_IN_PROGRESS') {
            uncertainCommitID = completedImportID;
            await resolveCommitUncertainty(completedImportID, rowCount);
            return;
          }
          if (!isUncertainCommitError(cause)) throw cause;
          uncertainCommitID = completedImportID;
          await resolveCommitUncertainty(completedImportID, rowCount);
        }
        return;
      }

      const requestRevision = workflowRevision;
      const requestWorkflowKey = currentWorkflowKey;
      const job = await uploadImport(selectedFile!, dataType as DataType);
      if (requestRevision !== workflowRevision || requestWorkflowKey !== currentWorkflowKey) return;
      const manualMapping = buildImportMapping(
        mappingRows.map((row) => ({ sourceColumn: row.sourceColumn, targetField: row.id }))
      );
      const nextAnalysis = await analyzeImport(job.id, manualMapping);
      if (requestRevision !== workflowRevision || requestWorkflowKey !== currentWorkflowKey) return;
      const resolvedMappingRows = mappingRows.map((row) => ({
        ...row,
        sourceColumn:
          nextAnalysis.mapping.find((column) => column.field === row.id)?.source_column ?? ''
      }));
      mappingRows = resolvedMappingRows;
      importID = job.id;
      nextAnalysis.error_report = {
        csv_url: `/api/v1/imports/${encodeURIComponent(job.id)}/errors?format=csv`,
        xlsx_url: `/api/v1/imports/${encodeURIComponent(job.id)}/errors?format=xlsx`
      };
      analysis = nextAnalysis;
      analysisRevision = nextAnalysis.analysis_revision ?? nextAnalysis.job.analysis_revision ?? '';
      commitAuthorizationKey = workflowKeyFor(resolvedMappingRows);
      commitIdempotencyKey = createIdempotencyKey();
      errorPage = 1;
      reviewTab = nextAnalysis.preview.can_commit ? 'preview' : 'errors';
      operationStatus = 'success';
      operationMessage = nextAnalysis.preview.can_commit
        ? 'Dosya kontrol edildi. Önizlemeyi inceleyip aktarımı tamamlayabilirsiniz.'
        : 'Dosya kontrol edildi. Hataları düzeltip dosyayı yeniden kontrol edin.';
      formErrors = { ...formErrors, operation: '' };
    } catch (cause) {
      operationStatus = 'idle';
      operationMessage = '';
      if (cause instanceof APIRequestError && cause.code === 'IMPORT_PREVIEW_STALE') {
        invalidateAnalysis();
      }
      const message = apiErrorMessage(cause);
      formErrors = { ...formErrors, operation: message };
      reviewTab = 'errors';
      await focusErrorSummary();
    } finally {
      isProcessing = false;
    }
  }

  onMount(() => {
    void loadCapabilities();
  });
</script>

<svelte:head><title>Aktarımlar · Varya One</title></svelte:head>

<section class="transfer-page" aria-labelledby="transfer-title">
  <header class="page-header transfer-header">
    <div>
      <h1 id="transfer-title">Aktarımlar</h1>
    </div>
  </header>

  <div class="transfer-layout">
    <div class="transfer-main">
      <section class="card transfer-card" aria-labelledby="setup-title">
        <div class="transfer-card-heading">
          <div>
            <h2 id="setup-title" class="panel-title">Aktarım ayarları</h2>
          </div>
          <Badge tone="neutral">
            {operationStatus === 'processing'
              ? 'Hazırlanıyor'
              : operationStatus === 'success'
                ? 'Hazır'
                : 'Hazır'}
          </Badge>
        </div>

        <form class="transfer-form" aria-label="Aktarım ayarları" onsubmit={startOperation}>
          {#if formErrorEntries.length}
            <Alert.Root
              variant="destructive"
              class="transfer-error-summary"
              role="alert"
              tabindex={-1}
              aria-labelledby="transfer-error-title"
              bind:ref={errorSummary}
            >
              <CircleAlert aria-hidden="true" />
              <div>
                <Alert.Title id="transfer-error-title">Formu kontrol edin</Alert.Title>
                <Alert.Description>
                  <ul>
                    {#each formErrorEntries as [id, message]}
                      <li><a href={`#${id}`}>{message}</a></li>
                    {/each}
                  </ul>
                </Alert.Description>
              </div>
            </Alert.Root>
          {/if}

          {#if capabilityState === 'error'}
            <Alert.Root variant="destructive" role="alert">
              <CircleAlert aria-hidden="true" />
              <div>
                <Alert.Title>Aktarım seçenekleri yüklenemedi</Alert.Title>
                <Alert.Description>{capabilityError}</Alert.Description>
              </div>
            </Alert.Root>
          {:else if capabilityState === 'loading'}
            <p class="transfer-capability-status" aria-live="polite">
              Yetkili aktarım seçenekleri yükleniyor…
            </p>
          {/if}

          <Field.FieldSet class="transfer-direction-fieldset">
            <Field.FieldLegend id="transfer-mode-label">Aktarım yönü</Field.FieldLegend>
            <div class="transfer-direction-options">
              <label
                class:selected={mode === 'import'}
                class:disabled={!canImportAny}
                class="transfer-direction-option"
              >
                <input
                  class="transfer-radio-input"
                  type="radio"
                  name="transfer-mode"
                  value="import"
                  checked={mode === 'import'}
                  disabled={!canImportAny}
                  onchange={() => updateMode('import')}
                />
                <span class="transfer-direction-copy">
                  <strong>İçe aktar</strong>
                  <small>CSV veya XLSX dosyasından veri yükleyin.</small>
                </span>
              </label>
              <label
                class:selected={mode === 'export'}
                class:disabled={!canExportAny}
                class="transfer-direction-option"
              >
                <input
                  class="transfer-radio-input"
                  type="radio"
                  name="transfer-mode"
                  value="export"
                  checked={mode === 'export'}
                  disabled={!canExportAny}
                  onchange={() => updateMode('export')}
                />
                <span class="transfer-direction-copy">
                  <strong>Dışa aktar</strong>
                  <small>Seçtiğiniz veri türü için dosya oluşturun.</small>
                </span>
              </label>
            </div>
          </Field.FieldSet>

          <Field.FieldGroup class="transfer-field-grid">
            <Field.Field data-invalid={capabilityState === 'ready' && !canUseCurrent}>
              <Field.Label for="data-type">Veri türü</Field.Label>
              <select
                id="data-type"
                class="transfer-select"
                value={dataType}
                onchange={(event) =>
                  updateDataType((event.currentTarget as HTMLSelectElement).value as DataType)}
                aria-describedby="data-type-error"
                aria-invalid={capabilityState === 'ready' && !canUseCurrent}
              >
                {#each selectableEntities as entity}
                  <option value={entity.type}>{entity.label}</option>
                {/each}
              </select>
              {#if capabilityState === 'ready' && !canUseCurrent && selectableEntities.length}
                <Field.Error id="data-type-error"
                  >Bu veri türü için aktarım kullanılamıyor.</Field.Error
                >
              {/if}
            </Field.Field>
          </Field.FieldGroup>

          {#if mode === 'import'}
            <div class="transfer-mode-content">
              <Field.Field data-invalid={Boolean(formErrors['file-upload'])}>
                <Field.Label for="file-upload">Dosya yükle</Field.Label>
                <div class="transfer-upload-row">
                  <Button type="button" variant="outline" onclick={chooseFile}>
                    <FileUp data-icon="inline-start" aria-hidden="true" /> Dosya seç
                  </Button>
                  {#if fileName}
                    <div class="transfer-selected-file" role="status">
                      <FileSpreadsheet aria-hidden="true" />
                      <span><strong>{fileName}</strong><small>{fileMeta}</small></span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        class="transfer-remove-file"
                        aria-label={`${fileName} dosyasını kaldır`}
                        onclick={clearFile}><X aria-hidden="true" /></Button
                      >
                    </div>
                  {:else}
                    <span class="transfer-file-empty">
                      CSV veya XLSX · En fazla {maxUploadBytes > 0
                        ? formatFileSize(maxUploadBytes)
                        : '—'}
                    </span>
                  {/if}
                </div>
                <input
                  bind:this={fileInput}
                  id="file-upload"
                  class="sr-only"
                  type="file"
                  accept=".csv,.xlsx,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
                  aria-describedby="file-upload-error"
                  aria-invalid={Boolean(formErrors['file-upload'])}
                  onchange={handleFileChange}
                />
                {#if formErrors['file-upload']}
                  <Field.Error id="file-upload-error">{formErrors['file-upload']}</Field.Error>
                {/if}
              </Field.Field>
            </div>
          {:else}
            <div class="transfer-mode-content">
              <Field.Field>
                <Field.Label for="export-format">Dosya biçimi</Field.Label>
                <select id="export-format" class="transfer-select" bind:value={exportFormat}>
                  <option value="csv">CSV</option>
                  <option value="xlsx">XLSX</option>
                </select>
              </Field.Field>
            </div>
          {/if}

          <div class="transfer-action-row">
            <div class="transfer-operation-status" aria-live="polite">
              {#if operationMessage}
                <CheckCircle2 aria-hidden="true" />
                <span>{operationMessage}</span>
              {:else if isProcessing}
                <LoaderCircle class="transfer-spinner" aria-hidden="true" />
                <span>Aktarım hazırlanıyor…</span>
              {:else}
                <span class="sr-only">İşlem için alanları doldurun.</span>
              {/if}
            </div>
            {#if formErrors.operation}
              <p id="operation" class="transfer-inline-error" role="alert" tabindex="-1">
                {formErrors.operation}
              </p>
            {/if}
            <Button type="submit" disabled={!canStartOperation} class="transfer-start-button">
              {#if isProcessing}<LoaderCircle
                  data-icon="inline-start"
                  class="transfer-spinner"
                  aria-hidden="true"
                /> Hazırlanıyor…{:else if mode === 'export'}<Download
                  data-icon="inline-start"
                  aria-hidden="true"
                /> Oluştur ve indir{:else if canCommitAnalysis}<CheckCircle2
                  data-icon="inline-start"
                  aria-hidden="true"
                /> Aktarımı tamamla{:else if uncertainCommitID}<CircleAlert
                  data-icon="inline-start"
                  aria-hidden="true"
                /> Durumu kontrol et{:else}<Table2 data-icon="inline-start" aria-hidden="true" /> Dosyayı
                kontrol et{/if}
            </Button>
          </div>
        </form>
      </section>

      <section class="card transfer-card transfer-review-card" aria-labelledby="review-title">
        <div class="transfer-card-heading">
          <div>
            <h2 id="review-title" class="panel-title">Kontrol alanı</h2>
          </div>
          {#if validationSummary}<Badge tone={validationSummary.errors ? 'warning' : 'success'}
              >{validationSummary.total} satır</Badge
            >{/if}
        </div>

        {#if mode === 'export'}
          <Empty.Root class="transfer-empty-state">
            <Empty.Media variant="icon"><Download aria-hidden="true" /></Empty.Media>
            <Empty.Header>
              <Empty.Title>Dosya oluşturmaya hazır</Empty.Title>
              <Empty.Description
                >Biçimi seçin, oluşturulan dosya otomatik indirilsin.</Empty.Description
              >
            </Empty.Header>
          </Empty.Root>
        {:else if !hasReviewSource}
          <Empty.Root class="transfer-empty-state">
            <Empty.Media variant="icon"><FileUp aria-hidden="true" /></Empty.Media>
            <Empty.Header>
              <Empty.Title>Dosya seçin</Empty.Title>
            </Empty.Header>
            <Empty.Content>
              <Button type="button" variant="outline" onclick={chooseFile}>
                <FileUp data-icon="inline-start" aria-hidden="true" /> Dosya yükle
              </Button>
            </Empty.Content>
          </Empty.Root>
        {:else}
          <Tabs.Root bind:value={reviewTab} class="transfer-review-tabs">
            <Tabs.List variant="line" aria-label="Aktarım kontrol sekmeleri">
              <Tabs.Trigger value="mapping"
                >Kolon eşleme <span class="transfer-tab-count"
                  >{mappedCount}/{mappingRows.length}</span
                ></Tabs.Trigger
              >
              <Tabs.Trigger value="preview">Önizleme</Tabs.Trigger>
              <Tabs.Trigger value="errors"
                >Hatalar <span class="transfer-tab-count transfer-tab-count-warning"
                  >{validationSummary?.errors ?? 0}</span
                ></Tabs.Trigger
              >
            </Tabs.List>

            <Tabs.Content value="mapping" class="transfer-review-content">
              <p class="transfer-content-intro">
                Zorunlu alanlar <span aria-label="zorunlu">*</span> ile işaretlidir.
              </p>
              <div
                id="mapping-table"
                class="transfer-mapping-list"
                aria-label="Kolon eşleme alanları"
              >
                {#each mappingRows as row (row.id)}
                  <article class="transfer-mapping-card">
                    <div class="transfer-mapping-card-heading">
                      <div>
                        <span class="transfer-target-name">
                          {row.label}{#if row.required}<span
                              class="transfer-required"
                              aria-hidden="true">*</span
                            >{/if}
                        </span>
                        <small id={`mapping-${row.id}-hint`} class="transfer-field-hint"
                          >Aktarım alanı: {row.label}</small
                        >
                      </div>
                      {#if row.sourceColumn.trim()}<Badge
                          tone={row.required ? 'success' : 'neutral'}
                          >{row.required ? 'Eşlendi' : 'İsteğe bağlı'}</Badge
                        >{:else if analysis}<Badge tone={row.required ? 'danger' : 'neutral'}
                          >Dosyada yok</Badge
                        >{:else}<Badge tone="warning">Otomatik eşlenecek</Badge>{/if}
                    </div>
                    <Input
                      id={`mapping-${row.id}`}
                      class="transfer-mapping-input"
                      value={row.sourceColumn}
                      placeholder={row.example || row.label}
                      aria-label={`${row.label} kaynak kolonu`}
                      aria-describedby={`mapping-${row.id}-hint`}
                      aria-invalid={row.required && Boolean(analysis) && !row.sourceColumn.trim()}
                      onchange={(event) =>
                        updateMapping(row.id, (event.currentTarget as HTMLInputElement).value)}
                    />
                  </article>
                {/each}
              </div>
              {#if formErrors.mapping}<p id="mapping" class="transfer-inline-error" role="alert">
                  {formErrors.mapping}
                </p>{/if}
            </Tabs.Content>

            <Tabs.Content value="preview" class="transfer-review-content">
              {#if !analysis}
                <Empty.Root class="transfer-empty-state transfer-empty-state-small">
                  <Empty.Media variant="icon"><Table2 aria-hidden="true" /></Empty.Media>
                  <Empty.Header>
                    <Empty.Title>Dosya henüz kontrol edilmedi</Empty.Title>
                    <Empty.Description>Eşlemeyi tamamlayıp dosyayı kontrol edin.</Empty.Description>
                  </Empty.Header>
                </Empty.Root>
              {:else}
                <div class="transfer-preview-meta" aria-live="polite">
                  <span><strong>{fileName}</strong> · Önizleme</span>
                  <Badge tone={analysis?.preview.can_commit ? 'success' : 'warning'}
                    >{analysis?.preview.can_commit ? 'Aktarıma hazır' : 'Kontrol gerekli'}</Badge
                  >
                </div>
                <div
                  id="preview-table"
                  class="transfer-preview-list"
                  aria-label="Aktarım önizlemesi"
                >
                  {#each previewRows as row}
                    <article
                      class:transfer-preview-row-invalid={!row.valid}
                      class="transfer-preview-row"
                    >
                      <div class="transfer-preview-row-heading">
                        <strong>Satır {row.rowNumber}</strong>
                        <Badge tone={row.valid ? 'success' : 'danger'}>{row.note}</Badge>
                      </div>
                      <dl class="transfer-preview-values">
                        {#each previewFields as field, index}
                          <div>
                            <dt>{field.label}</dt>
                            <dd>{row.values[index] ?? '—'}</dd>
                          </div>
                        {/each}
                      </dl>
                    </article>
                  {/each}
                  <p class="transfer-list-caption">İlk 20 satır gösteriliyor.</p>
                </div>
              {/if}
            </Tabs.Content>

            <Tabs.Content value="errors" class="transfer-review-content">
              {#if validationSummary?.errors}
                <Alert.Root variant="destructive" class="transfer-validation-alert" role="alert">
                  <CircleAlert aria-hidden="true" />
                  <div>
                    <Alert.Title>{validationSummary.errors} satırda kontrol gerekli</Alert.Title>
                    <Alert.Description
                      >Aktarımı tamamlamadan önce aşağıdaki kayıtları düzeltin veya eşlemeyi
                      güncelleyin.</Alert.Description
                    >
                  </div>
                </Alert.Root>
                <ul class="transfer-error-list" aria-live="polite">
                  {#each paginatedErrorRows as row}
                    <li>
                      <span>Satır {row.row_number} · {currentEntityLabel}</span>
                      <strong>{row.issues?.[0]?.message ?? 'Satır kontrolü başarısız.'}</strong>
                      <button
                        type="button"
                        class="transfer-error-link"
                        onclick={() => void focusIssue(row.issues?.[0]?.field)}
                        >{row.issues?.[0]?.field ? 'Alanı düzelt' : 'Önizlemeyi aç'}</button
                      >
                    </li>
                  {/each}
                </ul>
                {#if errorPageCount > 1}
                  <nav class="transfer-error-pagination" aria-label="Hata sayfaları">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={errorPage === 1}
                      onclick={() => (errorPage = Math.max(1, errorPage - 1))}>Önceki</Button
                    >
                    <span aria-live="polite">Sayfa {errorPage} / {errorPageCount}</span>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={errorPage === errorPageCount}
                      onclick={() => (errorPage = Math.min(errorPageCount, errorPage + 1))}
                      >Sonraki</Button
                    >
                  </nav>
                {/if}
                {#if analysis?.error_report?.csv_url || analysis?.error_report?.xlsx_url}
                  <div class="transfer-error-reports" aria-label="Hata raporları">
                    <span>Hata raporu:</span>
                    {#if analysis.error_report.csv_url}<a href={analysis.error_report.csv_url}
                        >CSV indir</a
                      >{/if}
                    {#if analysis.error_report.xlsx_url}<a href={analysis.error_report.xlsx_url}
                        >XLSX indir</a
                      >{/if}
                  </div>
                {/if}
              {:else if !analysis}
                <Empty.Root class="transfer-empty-state transfer-empty-state-small">
                  <Empty.Media variant="icon"><Table2 aria-hidden="true" /></Empty.Media>
                  <Empty.Header><Empty.Title>Dosya kontrol edilmedi</Empty.Title></Empty.Header>
                </Empty.Root>
              {:else}
                <Empty.Root class="transfer-empty-state transfer-empty-state-small">
                  <Empty.Media variant="icon"><CheckCircle2 aria-hidden="true" /></Empty.Media>
                  <Empty.Header><Empty.Title>Hata bulunmadı</Empty.Title></Empty.Header>
                </Empty.Root>
              {/if}
            </Tabs.Content>
          </Tabs.Root>

          {#if validationSummary}
            <div class="transfer-summary" aria-label="Doğrulama özeti">
              <div class="transfer-summary-heading">
                <h3>Doğrulama özeti</h3>
                <span>Sunucu doğrulaması</span>
              </div>
              <div class="transfer-summary-grid">
                <div class="transfer-metric">
                  <span>Toplam satır</span><strong>{validationSummary.total}</strong>
                </div>
                <div class="transfer-metric transfer-metric-success">
                  <span>Geçerli</span><strong>{validationSummary.valid}</strong>
                </div>
                <div class="transfer-metric transfer-metric-warning">
                  <span>Uyarı</span><strong>{validationSummary.warnings}</strong>
                </div>
                <div class="transfer-metric transfer-metric-danger">
                  <span>Hata</span><strong>{validationSummary.errors}</strong>
                </div>
              </div>
            </div>
          {/if}
        {/if}
      </section>
    </div>
  </div>
</section>

<style>
  .transfer-page,
  .transfer-layout,
  .transfer-main,
  .transfer-card,
  .transfer-card-heading,
  .transfer-form,
  .transfer-upload-row,
  .transfer-action-row,
  .transfer-preview-meta,
  .transfer-summary {
    min-width: 0;
  }

  .transfer-page {
    display: grid;
    gap: 14px;
  }

  .transfer-header {
    margin-bottom: 0;
  }

  .transfer-layout {
    display: grid;
    gap: 12px;
    align-items: start;
  }

  .transfer-main {
    display: grid;
    gap: 12px;
    min-width: 0;
  }

  .transfer-card {
    min-width: 0;
    padding: 0;
    overflow: hidden;
  }

  .transfer-card-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    padding: 16px 16px 0;
  }

  .transfer-card-heading .panel-title {
    margin-bottom: 0;
  }

  .transfer-card-heading > *,
  .transfer-summary-heading > * {
    min-width: 0;
  }

  .transfer-form {
    display: grid;
    gap: 16px;
    padding: 16px;
  }

  .transfer-capability-status {
    margin: 0 16px;
    color: var(--text-muted);
    font-size: 12px;
  }

  :global(.transfer-field-grid) {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 14px;
  }

  .transfer-select {
    width: 100%;
    min-height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    padding: 0 10px;
    background: var(--surface);
    color: var(--text);
    font-size: 14px;
  }

  .transfer-select:focus-visible {
    border-color: var(--primary);
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }

  :global(.transfer-review-tabs) {
    min-width: 0;
  }

  :global(.transfer-review-tabs [data-slot='tabs-list']) {
    max-width: 100%;
    min-width: 0;
    width: 100%;
    height: auto;
    flex-wrap: wrap;
    justify-content: flex-start;
    overflow: visible;
  }

  :global(.transfer-review-tabs [data-slot='tabs-trigger']) {
    max-width: 100%;
    min-height: 44px;
    min-width: 0;
    overflow-wrap: anywhere;
    white-space: normal;
  }

  :global(.transfer-direction-fieldset) {
    min-width: 0;
    gap: 8px;
  }

  .transfer-direction-options {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px;
  }

  .transfer-direction-option {
    display: flex;
    min-height: 56px;
    align-items: center;
    gap: 10px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    padding: 10px 12px;
    background: var(--surface);
    color: var(--text-muted);
    cursor: pointer;
    transition:
      border-color 120ms ease,
      background-color 120ms ease,
      color 120ms ease;
  }

  .transfer-direction-option:hover {
    border-color: var(--primary);
    color: var(--text);
  }

  .transfer-direction-option.disabled,
  .transfer-direction-option.disabled:hover {
    border-color: var(--border);
    color: var(--text-muted);
    cursor: not-allowed;
    opacity: 0.6;
  }

  .transfer-direction-option.selected {
    border-color: var(--primary);
    background: var(--primary-soft);
    color: var(--text);
  }

  .transfer-radio-input {
    width: 18px;
    height: 18px;
    flex: 0 0 auto;
    margin: 0;
    accent-color: var(--primary);
  }

  .transfer-radio-input:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }

  .transfer-direction-copy {
    display: grid;
    min-width: 0;
    gap: 2px;
  }

  .transfer-direction-copy strong {
    color: var(--text);
    font-size: 13px;
  }

  .transfer-direction-copy small {
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1.35;
  }

  :global(.transfer-mode-content) {
    padding-top: 16px;
  }

  .transfer-upload-row {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
  }

  .transfer-file-empty {
    color: var(--text-muted);
    font-size: 12px;
  }

  .transfer-selected-file {
    display: flex;
    min-width: 0;
    align-items: center;
    gap: 8px;
    color: var(--primary);
  }

  .transfer-selected-file > :global(svg) {
    flex: 0 0 auto;
  }

  .transfer-selected-file span {
    display: grid;
    min-width: 0;
    gap: 2px;
  }

  .transfer-selected-file strong,
  .transfer-selected-file small {
    overflow-wrap: anywhere;
  }

  .transfer-selected-file strong {
    color: var(--text);
    font-size: 13px;
  }

  .transfer-selected-file small {
    color: var(--text-muted);
    font-size: 11px;
  }

  :global(.transfer-remove-file) {
    flex: 0 0 auto;
    min-height: 40px;
    min-width: 40px;
  }

  :global(.transfer-error-summary) {
    border-color: color-mix(in srgb, var(--danger) 35%, var(--border));
  }

  :global(.transfer-error-summary [data-slot='alert-description']) {
    color: var(--danger);
  }

  :global(.transfer-error-summary ul) {
    display: grid;
    gap: 4px;
    margin: 4px 0 0;
    padding-left: 18px;
  }

  :global(.transfer-error-summary a) {
    color: var(--danger);
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  .transfer-action-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    border-top: 1px solid var(--border);
    padding-top: 14px;
  }

  .transfer-operation-status {
    display: flex;
    min-width: 0;
    align-items: flex-start;
    gap: 7px;
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1.45;
  }

  .transfer-operation-status :global(svg) {
    flex: 0 0 auto;
    margin-top: 1px;
    color: var(--primary);
  }

  .transfer-operation-status :global(.transfer-spinner) {
    color: var(--info);
  }

  :global(.transfer-start-button) {
    min-height: 44px;
  }

  :global(.transfer-review-content) {
    display: grid;
    gap: 12px;
    padding: 16px;
  }

  .transfer-content-intro {
    margin: 0;
    color: var(--text-muted);
    font-size: 12px;
    line-height: 1.5;
  }

  .transfer-content-intro span,
  .transfer-required {
    color: var(--danger);
    font-weight: 700;
  }

  .transfer-mapping-list,
  .transfer-preview-list {
    display: grid;
    min-width: 0;
    gap: 8px;
  }

  .transfer-mapping-card,
  .transfer-preview-row {
    display: grid;
    min-width: 0;
    gap: 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 12px;
    background: var(--surface);
  }

  .transfer-mapping-card:focus-within,
  .transfer-preview-row:focus-within {
    border-color: var(--primary);
  }

  .transfer-mapping-card-heading,
  .transfer-preview-row-heading {
    display: flex;
    min-width: 0;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px;
  }

  .transfer-mapping-card-heading > :first-child,
  .transfer-preview-row-heading > :first-child {
    min-width: 0;
  }

  .transfer-preview-row-heading strong {
    color: var(--text);
    font-size: 12px;
  }

  .transfer-preview-row-invalid {
    border-color: color-mix(in srgb, var(--danger) 35%, var(--border));
  }

  .transfer-preview-values {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px 12px;
    margin: 0;
  }

  .transfer-preview-values > div {
    min-width: 0;
  }

  .transfer-preview-values dt {
    margin-bottom: 3px;
    color: var(--text-muted);
    font-size: 10px;
  }

  .transfer-preview-values dd {
    margin: 0;
    color: var(--text);
    font-size: 12px;
    overflow-wrap: anywhere;
  }

  .transfer-list-caption {
    margin: 2px 0 0;
    color: var(--text-muted);
    font-size: 11px;
    overflow-wrap: anywhere;
  }

  :global(.transfer-mapping-input) {
    width: 100%;
    min-width: 0;
    max-width: 100%;
  }

  .transfer-target-name {
    display: block;
    color: var(--text);
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .transfer-field-hint {
    display: block;
    margin-top: 3px;
    color: var(--text-muted);
    font-size: 10px;
    overflow-wrap: anywhere;
  }

  .transfer-inline-error {
    margin: 0;
    color: var(--danger);
    font-size: 12px;
    overflow-wrap: anywhere;
  }

  .transfer-preview-meta {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    color: var(--text-muted);
    font-size: 11px;
  }

  .transfer-preview-meta strong {
    color: var(--text);
    font-size: 12px;
    overflow-wrap: anywhere;
  }

  .transfer-tab-count {
    display: inline-flex;
    flex: 0 0 auto;
    min-width: 22px;
    justify-content: center;
    border-radius: 999px;
    background: var(--primary-soft);
    color: var(--primary);
    font-size: 10px;
    white-space: nowrap;
  }

  .transfer-tab-count-warning {
    background: color-mix(in srgb, var(--warning) 14%, var(--surface));
    color: var(--warning);
  }

  :global(.transfer-validation-alert) {
    align-items: flex-start;
    border-color: color-mix(in srgb, var(--danger) 35%, var(--border));
  }

  :global(.transfer-validation-alert [data-slot='alert-description']) {
    color: var(--text-muted);
  }

  .transfer-error-list {
    display: grid;
    gap: 0;
    margin: 0;
    padding: 0;
    list-style: none;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
  }

  .transfer-error-list li {
    display: grid;
    grid-template-columns: minmax(0, 0.8fr) minmax(0, 1.4fr) auto;
    gap: 12px;
    align-items: center;
    padding: 10px 12px;
    border-bottom: 1px solid var(--border);
    font-size: 11px;
    min-width: 0;
  }

  .transfer-error-list li > * {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .transfer-error-list li:last-child {
    border-bottom: 0;
  }

  .transfer-error-list span {
    color: var(--text-muted);
  }

  .transfer-error-list strong {
    color: var(--text);
    font-weight: 600;
  }

  .transfer-error-link {
    border: 0;
    padding: 0;
    background: transparent;
    color: var(--primary);
    cursor: pointer;
    font: inherit;
    text-align: left;
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  .transfer-error-link:focus-visible {
    outline: 2px solid var(--primary);
    outline-offset: 2px;
  }

  .transfer-error-pagination,
  .transfer-error-reports {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    color: var(--text-muted);
    font-size: 11px;
  }

  .transfer-error-reports a {
    color: var(--primary);
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  .transfer-summary {
    border-top: 1px solid var(--border);
    padding: 14px 16px 16px;
  }

  .transfer-summary-heading {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
    margin-bottom: 10px;
  }

  .transfer-summary-heading h3 {
    margin: 0;
    font-size: 13px;
  }

  .transfer-summary-heading span {
    color: var(--text-muted);
    font-size: 11px;
  }

  .transfer-summary-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 8px;
  }

  .transfer-metric {
    display: grid;
    gap: 5px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 9px 10px;
  }

  .transfer-metric span {
    color: var(--text-muted);
    font-size: 10px;
  }

  .transfer-metric strong {
    font-size: 18px;
    line-height: 1;
  }

  .transfer-metric-success {
    border-color: color-mix(in srgb, var(--success) 30%, var(--border));
  }

  .transfer-metric-success strong {
    color: var(--success);
  }

  .transfer-metric-warning {
    border-color: color-mix(in srgb, var(--warning) 30%, var(--border));
  }

  .transfer-metric-warning strong {
    color: var(--warning);
  }

  .transfer-metric-danger {
    border-color: color-mix(in srgb, var(--danger) 30%, var(--border));
  }

  .transfer-metric-danger strong {
    color: var(--danger);
  }

  :global(.transfer-empty-state) {
    min-height: 220px;
    margin: 16px;
  }

  :global(.transfer-empty-state-small) {
    min-height: 150px;
  }

  :global(.transfer-spinner) {
    animation: transfer-spin 0.9s linear infinite;
  }

  @keyframes transfer-spin {
    to {
      transform: rotate(360deg);
    }
  }

  @media (max-width: 1100px) {
    .transfer-layout {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 720px) {
    :global(.transfer-field-grid) {
      grid-template-columns: 1fr;
    }

    .transfer-direction-options {
      grid-template-columns: 1fr;
    }

    .transfer-action-row {
      align-items: stretch;
      flex-direction: column;
    }

    :global(.transfer-start-button) {
      width: 100%;
    }

    .transfer-summary-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .transfer-error-list li {
      grid-template-columns: 1fr;
      gap: 4px;
    }

    .transfer-preview-meta {
      align-items: flex-start;
      flex-direction: column;
    }

    .transfer-preview-values {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 480px) {
    .transfer-card-heading,
    .transfer-form,
    :global(.transfer-review-content),
    .transfer-summary {
      padding-left: 12px;
      padding-right: 12px;
    }

    .transfer-card-heading {
      flex-direction: column;
    }

    .transfer-summary-grid {
      gap: 6px;
    }

    .transfer-metric {
      padding: 8px;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    :global(.transfer-spinner) {
      animation: none;
    }
  }
</style>

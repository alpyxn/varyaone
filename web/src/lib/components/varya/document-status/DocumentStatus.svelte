<script lang="ts">
  import { Badge } from '$lib/components/ui/badge';
  let { status }: { status: string } = $props();
  const labels: Record<string, string> = {
    DRAFT: 'Taslak',
    ACTIVE: 'Aktif',
    INACTIVE: 'Pasif',
    APPROVED: 'Onaylandı',
    POSTED: 'İşlendi',
    ISSUED: 'Kesildi',
    CANCELLED: 'İptal',
    REVERSED: 'Ters Kayıt',
    SENT: 'Gönderildi',
    ACCEPTED: 'Kabul edildi',
    REJECTED: 'Reddedildi',
    EXPIRED: 'Süresi doldu',
    CONFIRMED: 'Onaylandı',
    PARTIALLY_FULFILLED: 'Kısmi karşılandı',
    FULFILLED: 'Tamamlandı'
  };
  const tone = $derived(
    status === 'ACTIVE' ||
      status === 'APPROVED' ||
      status === 'POSTED' ||
      status === 'ISSUED' ||
      status === 'ACCEPTED' ||
      status === 'FULFILLED'
      ? 'success'
      : status === 'CANCELLED' || status === 'INACTIVE'
        ? 'danger'
        : status === 'DRAFT'
          ? 'neutral'
          : 'warning'
  );
</script>

<Badge {tone}>{labels[status] ?? status}</Badge>

/** Keep native browser validation in Turkish even when the browser is English. */
export function installTurkishFormValidation(document: Document): () => void {
  const translated = new WeakSet<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>();
  function control(event: Event) {
    const target = event.target;
    return target instanceof HTMLInputElement ||
      target instanceof HTMLSelectElement ||
      target instanceof HTMLTextAreaElement
      ? target
      : undefined;
  }
  function invalid(event: Event) {
    const target = control(event);
    if (!target) return;
    if (translated.has(target)) target.setCustomValidity('');
    const validity = target.validity;
    // Preserve custom validation owned by individual forms.
    if (validity.customError) return;
    let message = '';
    if (validity.valueMissing)
      message =
        target instanceof HTMLSelectElement
          ? 'Lütfen bir seçenek seçin.'
          : target instanceof HTMLInputElement && ['checkbox', 'radio'].includes(target.type)
            ? 'Devam etmek için bu seçeneği işaretleyin.'
            : 'Lütfen bu alanı doldurun.';
    else if (validity.typeMismatch)
      message =
        target instanceof HTMLInputElement && target.type === 'email'
          ? 'Geçerli bir e-posta adresi girin.'
          : 'Geçerli bir internet adresi girin.';
    else if (validity.badInput) message = 'Geçerli bir değer girin.';
    else if (validity.rangeUnderflow) message = 'Girilen değer izin verilen alt sınırın altında.';
    else if (validity.rangeOverflow) message = 'Girilen değer izin verilen üst sınırı aşıyor.';
    else if (validity.stepMismatch) message = 'İzin verilen aralığa uygun bir değer girin.';
    else if (validity.tooShort)
      message = 'Girilen metin çok kısa. Lütfen daha fazla karakter girin.';
    else if (validity.tooLong) message = 'Girilen metin çok uzun. Lütfen metni kısaltın.';
    else if (validity.patternMismatch) message = 'Lütfen istenen biçime uygun bir değer girin.';
    if (message) {
      target.setCustomValidity(message);
      translated.add(target);
    }
  }
  function clear(event: Event) {
    const target = control(event);
    if (target && translated.has(target)) {
      target.setCustomValidity('');
      translated.delete(target);
    }
  }
  document.addEventListener('invalid', invalid, true);
  document.addEventListener('input', clear, true);
  document.addEventListener('change', clear, true);
  // A programmatic form reset emits no input/change events.
  function reset(event: Event) {
    if (event.target instanceof HTMLFormElement)
      for (const target of event.target.elements) {
        if (
          (target instanceof HTMLInputElement ||
            target instanceof HTMLSelectElement ||
            target instanceof HTMLTextAreaElement) &&
          translated.has(target)
        ) {
          target.setCustomValidity('');
          translated.delete(target);
        }
      }
  }
  document.addEventListener('reset', reset, true);
  return () => {
    document.removeEventListener('invalid', invalid, true);
    document.removeEventListener('input', clear, true);
    document.removeEventListener('change', clear, true);
    document.removeEventListener('reset', reset, true);
  };
}

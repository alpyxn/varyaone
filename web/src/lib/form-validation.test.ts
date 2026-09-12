import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { installTurkishFormValidation } from './form-validation';
let cleanup: () => void;
beforeEach(() => {
  cleanup = installTurkishFormValidation(document);
});
afterEach(() => {
  cleanup();
  document.body.innerHTML = '';
});
describe('native form validation in Turkish', () => {
  it('translates required fields and clears its error after input', () => {
    document.body.innerHTML = '<form><input required></form>';
    const input = document.querySelector('input')!;
    expect(input.checkValidity()).toBe(false);
    expect(input.validationMessage).toBe('Lütfen bu alanı doldurun.');
    input.value = 'Ada';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    expect(input.checkValidity()).toBe(true);
  });
  it('translates invalid email addresses', () => {
    document.body.innerHTML = '<input type="email" value="invalid">';
    const input = document.querySelector('input')!;
    expect(input.checkValidity()).toBe(false);
    expect(input.validationMessage).toBe('Geçerli bir e-posta adresi girin.');
  });
  it('preserves application validation and resets native translations', () => {
    document.body.innerHTML = '<form><input required><input></form>';
    const [required, custom] = document.querySelectorAll('input');
    custom.setCustomValidity('Bu kod zaten kullanılıyor.');
    required.checkValidity();
    custom.checkValidity();
    expect(custom.validationMessage).toBe('Bu kod zaten kullanılıyor.');
    document.querySelector('form')!.reset();
    expect(required.validity.customError).toBe(false);
    expect(custom.validity.customError).toBe(true);
  });
});

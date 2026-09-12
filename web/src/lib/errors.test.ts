import { describe, expect, it } from 'vitest';
import { errorMessage, DEFAULT_ERROR_MESSAGE } from './errors';
import { APIRequestError } from './api';
import catalog from './error-messages.json';

describe('Turkish UI errors', () => {
  it.each([
    'INSUFFICIENT_STOCK',
    'SERIAL_ALREADY_IN_STOCK',
    'VARIANT_REQUIRED',
    'STOCK_COUNT_ALREADY_POSTED'
  ] as const)('explains %s instead of showing the internal code', (code) => {
    expect(
      new APIRequestError({ code, message: code, details: {}, trace_id: '' }, 422).message
    ).toBe(catalog[code]);
  });
  it.each([
    'Failed to fetch',
    'NetworkError when attempting to fetch resource.',
    'Load failed',
    'offline'
  ])('localizes browser network error %s', (message) => {
    expect(errorMessage(new TypeError(message))).toBe(catalog.NETWORK_ERROR);
  });
  it.each([
    'Cannot read properties of undefined',
    'SyntaxError: Unexpected token',
    'invalid quantity "İstanbul"',
    'UNKNOWN_ERROR',
    '<html>Bad Gateway</html>'
  ])('does not expose raw exception %s', (message) => {
    expect(errorMessage(new Error(message))).toBe(DEFAULT_ERROR_MESSAGE);
  });
  it('preserves useful Turkish details and structured metadata', () => {
    const details = {
      errors: [{ field: 'quantity', message: 'must be positive' }],
      available: '2'
    };
    const error = new APIRequestError(
      {
        code: 'INSUFFICIENT_STOCK',
        message: 'Seçilen depoda yalnızca 2 adet var. Miktarı kontrol edin.',
        details,
        trace_id: 'trace-1'
      },
      409
    );
    expect(error.message).toBe('Seçilen depoda yalnızca 2 adet var. Miktarı kontrol edin.');
    expect(error.details).toBe(details);
    expect(error.code).toBe('INSUFFICIENT_STOCK');
    expect(error.traceId).toBe('trace-1');
    expect(error.status).toBe(409);
  });
  it('strips internal validation prefixes', () => {
    expect(errorMessage('validation failed: Barkod gereklidir.')).toBe('Barkod gereklidir.');
  });
  it('uses action-specific Turkish fallback for unknown errors', () => {
    expect(errorMessage(new Error('Something went wrong'), 'Dosya okunamadı.')).toBe(
      'Dosya okunamadı.'
    );
  });
  it.each(Object.values(catalog))('preserves catalog message: %s', (message) => {
    expect(errorMessage(message)).toBe(message);
  });
});

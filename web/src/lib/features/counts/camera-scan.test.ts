import { describe, expect, it } from 'vitest';
import { acceptCameraFrame, createCameraScanState, normalizeDetectedValue } from './camera-scan';

describe('camera barcode scan helpers', () => {
  it('normalizes detector values without depending on browser APIs', () => {
    expect(normalizeDetectedValue('  8690001  ')).toBe('8690001');
    expect(normalizeDetectedValue({ rawValue: '  8690001 ' })).toBe('8690001');
    expect(normalizeDetectedValue({ rawValue: '\uFF11\uFF12\uFF13' })).toBe('123');
    expect(normalizeDetectedValue(undefined)).toBe('');
    expect(normalizeDetectedValue({ rawValue: 8690001 })).toBe('');
  });

  it('accepts exactly one unique non-empty barcode', () => {
    const result = acceptCameraFrame(['', ' 8690001 ', { rawValue: '8690001' }]);

    expect(result).toMatchObject({ accepted: true, barcode: '8690001', reason: 'accepted' });
    expect(result.state.lastAcceptedKey).toBe('8690001');
  });

  it('rejects empty frames without changing state', () => {
    const state = { lastAcceptedKey: '8690001', lastAcceptedAt: 1000 };
    const result = acceptCameraFrame([' ', undefined, null], state, 1100);

    expect(result).toEqual({ accepted: false, reason: 'empty', state });
  });

  it('rejects frames containing more than one unique barcode', () => {
    const state = createCameraScanState();
    const result = acceptCameraFrame(['8690001', '8690002'], state, 1000);

    expect(result).toEqual({ accepted: false, reason: 'ambiguous', state });
  });

  it('treats duplicate values that differ only by whitespace or case as one barcode', () => {
    const result = acceptCameraFrame([' abC-1 ', 'ABC-1'], createCameraScanState(), 1000);

    expect(result).toMatchObject({ accepted: true, barcode: 'abC-1' });
  });

  it('suppresses the same barcode during the cooldown window', () => {
    const first = acceptCameraFrame(['8690001'], createCameraScanState(), 1000, 1500);
    const repeated = acceptCameraFrame([' 8690001 '], first.state, 2499, 1500);
    const afterCooldown = acceptCameraFrame(['8690001'], repeated.state, 2500, 1500);

    expect(first.accepted).toBe(true);
    expect(repeated).toMatchObject({ accepted: false, barcode: '8690001', reason: 'cooldown' });
    expect(afterCooldown).toMatchObject({ accepted: true, barcode: '8690001', reason: 'accepted' });
  });

  it('does not let rejected ambiguous frames extend the cooldown', () => {
    const first = acceptCameraFrame(['8690001'], createCameraScanState(), 1000, 1500);
    const ambiguous = acceptCameraFrame(['8690001', '8690002'], first.state, 1200, 1500);
    const valid = acceptCameraFrame(['8690001'], ambiguous.state, 2500, 1500);

    expect(ambiguous.accepted).toBe(false);
    expect(valid.accepted).toBe(true);
  });

  it('uses the configured cooldown and safely handles invalid timing values', () => {
    const first = acceptCameraFrame(['8690001'], createCameraScanState(), 1000, 0);
    const immediate = acceptCameraFrame(['8690001'], first.state, 1000, 0);
    const invalid = acceptCameraFrame(['8690002'], first.state, Number.NaN, Number.NaN);

    expect(immediate.accepted).toBe(true);
    expect(invalid).toMatchObject({ accepted: true, barcode: '8690002' });
  });
});

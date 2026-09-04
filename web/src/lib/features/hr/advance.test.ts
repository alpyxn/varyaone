import { describe, expect, it } from 'vitest';
import {
  advanceActionVisibility,
  localTodayISO,
  normalizeTRYAmount,
  validTRYAmount
} from './advance';

describe('employee advance UI rules', () => {
  it('accepts familiar positive TRY inputs and normalizes the API value', () => {
    expect(validTRYAmount('1250.00')).toBe(true);
    expect(validTRYAmount('1250')).toBe(true);
    expect(validTRYAmount('1250,5')).toBe(true);
    expect(normalizeTRYAmount('1250')).toBe('1250.00');
    expect(normalizeTRYAmount('1250,5')).toBe('1250.50');
    // Turkish notation: the dot groups the thousands, so "1.234" is the
    // amount the user read off the screen, not one lira and change.
    expect(normalizeTRYAmount('1.234')).toBe('1234.00');
    expect(normalizeTRYAmount('1.234,50')).toBe('1234.50');
    expect(validTRYAmount('1.2345')).toBe(false);
    expect(validTRYAmount('0.00')).toBe(false);
  });
  it('uses the browser-local business day instead of a UTC slice', () => {
    expect(localTodayISO(new Date(2026, 7, 30, 1, 0))).toBe('2026-08-30');
  });
  it('hides write actions without their explicit permission or on closed advances', () => {
    expect(advanceActionVisibility(['hr.employee_advance.collect'], 'OPEN').collect).toBe(true);
    expect(advanceActionVisibility(['hr.employee_advance.collect'], 'OPEN').writeOff).toBe(false);
    expect(advanceActionVisibility(['hr.employee_advance.writeoff'], 'CLOSED').writeOff).toBe(
      false
    );
  });
});

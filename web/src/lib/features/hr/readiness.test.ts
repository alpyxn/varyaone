import { describe, expect, it } from 'vitest';
import {
  payrollErrorDetails,
  payrollErrorInfo,
  timesheetBlocker,
  type EmployeeReadiness
} from './types';

const readiness = (issues: EmployeeReadiness['issues']): EmployeeReadiness => ({
  employee_id: 'e1',
  employee_code: 'P-1',
  name: 'Ayşe Yılmaz',
  issues,
  timesheet_ready: !issues.some((i) => i.blocks === 'TIMESHEET'),
  payroll_ready: issues.length === 0
});

describe('timesheetBlocker', () => {
  it('reports only the issue that blocks the puantaj', () => {
    const blocked = readiness([
      {
        code: 'EMPLOYEE_NO_EMPLOYMENT',
        message: 'İşe giriş tarihi girilmemiş.',
        blocks: 'TIMESHEET',
        tab: 'employment'
      }
    ]);
    expect(timesheetBlocker(blocked)).toBe('İşe giriş tarihi girilmemiş.');

    const wageOnly = readiness([
      { code: 'EMPLOYEE_NO_WAGE', message: 'Ücret tanımı yok.', blocks: 'PAYROLL', tab: 'wage' }
    ]);
    expect(timesheetBlocker(wageOnly)).toBe('');
    expect(timesheetBlocker(undefined)).toBe('');
  });
});

describe('payroll failure messages', () => {
  it('explains a missing wage instead of "kapsam dışında"', () => {
    const [detail] = payrollErrorDetails([
      {
        code: 'PAYROLL_POPULATION_NOT_SUPPORTED',
        field: 'employee_no_wage',
        message: 'Ücret tanımı yok. Ücret sekmesinden brüt ücreti girin.'
      }
    ]);
    const info = payrollErrorInfo(detail);
    expect(info.title).toBe('Ücret tanımı yok');
    expect(info.hint).toContain('Ücret sekmesinden');
  });

  it('names the field the engine rejected rather than "kapsam dışında"', () => {
    const info = payrollErrorInfo({
      code: 'PAYROLL_POPULATION_NOT_SUPPORTED',
      field: 'wage_basis',
      message: 'ücret aylık brüt olarak tanımlanmalı'
    });
    expect(info.title).toBe('Ücret aylık brüt tanımlanmalı');
  });

  it('falls back to the message the server sent when nothing is mapped', () => {
    const info = payrollErrorInfo({ code: 'SOMETHING_NEW', message: 'yeni bir hata' });
    expect(info.title).toBe('yeni bir hata');
  });
});

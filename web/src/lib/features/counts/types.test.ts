import { describe, expect, it } from 'vitest';
import { normalizeCount } from './types';

describe('sayım görünümü normalizasyonu', () => {
  it('does not keep a resolved line exception as an active blocker', () => {
    const count = normalizeCount({
      id: 'count-1',
      status: 'REVIEW',
      lines: [
        {
          id: 'line-1',
          product_name: 'Karton Kutu',
          exception: 'İnceleme gerekli'
        }
      ],
      exceptions: [
        {
          id: 'exception-1',
          scope_id: 'line-1',
          status: 'RESOLVED',
          severity: 'error',
          message: 'İnceleme tamamlandı'
        }
      ]
    });

    expect(count.lines[0].exception).toBeUndefined();
  });
});

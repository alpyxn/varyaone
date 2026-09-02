import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  clearScanQueueForTests,
  enqueueScanEvent,
  flushScanQueue,
  readScanQueue
} from './scan-queue';
import { makeScanEvent } from './scan-utils';

describe('stock count scan queue', () => {
  beforeEach(async () => {
    await clearScanQueueForTests();
  });

  it('deduplicates repeated enqueue and retries with the same stable event id', async () => {
    const event = makeScanEvent('8690001', '2', 'scan:stable-event');
    await enqueueScanEvent({
      count_id: 'count-1',
      session_id: 'session-1',
      event,
      status: 'pending'
    });
    await enqueueScanEvent({
      count_id: 'count-1',
      session_id: 'session-1',
      event,
      status: 'pending'
    });
    expect(await readScanQueue('count-1')).toHaveLength(1);

    const send = vi
      .fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(undefined);
    const first = await flushScanQueue('count-1', send);
    expect(first.failed).toBe(1);
    expect((await readScanQueue('count-1'))[0].event.event_id).toBe('scan:stable-event');

    const second = await flushScanQueue('count-1', send);
    expect(second.sent).toBe(1);
    expect(await readScanQueue('count-1')).toHaveLength(0);
    expect(send.mock.calls[0][1][0].event_id).toBe(send.mock.calls[1][1][0].event_id);
  });
});

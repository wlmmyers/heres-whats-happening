import { describe, expect, it, vi } from 'vitest';
import { handleSQS, isFunctionUrlEvent, isSQSEvent } from './handler.js';
import type { EventMessage } from './schema.js';

const sqsEvent = {
  Records: [
    {
      messageId: 'm1',
      eventSource: 'aws:sqs',
      body: JSON.stringify({
        source_id: 'ticketmaster',
        source_event_id: 'tm-aaa',
        title: 'La Luz',
        starts_at: '2026-09-02T20:00:00Z',
        venue: { name: 'The Chapel' },
        performers: ['La Luz'],
      }),
    },
  ],
};

const s3Event = {
  Records: [{ eventSource: 'aws:s3', s3: { bucket: { name: 'b' }, object: { key: 'k' } } }],
};

describe('isSQSEvent', () => {
  // The S3 branch also uses `Records`, so presence of that key cannot be the
  // discriminator — handler.ts previously cast anything non-FunctionURL to
  // S3Event, which would have happily accepted an SQS event.
  it('distinguishes SQS from S3 by eventSource', () => {
    expect(isSQSEvent(sqsEvent as never)).toBe(true);
    expect(isSQSEvent(s3Event as never)).toBe(false);
  });

  it('does not claim a Function URL event', () => {
    const url = { version: '2.0', requestContext: { http: { method: 'POST' } } };
    expect(isSQSEvent(url as never)).toBe(false);
    expect(isFunctionUrlEvent(url as never)).toBe(true);
  });
});

describe('handleSQS', () => {
  it('enriches each record and emits it to the enriched queue', async () => {
    const emitted: EventMessage[] = [];
    await handleSQS(sqsEvent as never, {
      enrich: async (m) => ({
        ...m,
        enrichment: {
          attempted_at: '2026-08-12T00:00:00Z',
          artist: { performer: 'La Luz', display_name: 'La Luz', status: 'ok' },
        },
      }),
      emit: async (msgs) => {
        emitted.push(...(msgs as EventMessage[]));
      },
    });

    expect(emitted).toHaveLength(1);
    expect(emitted[0].title).toBe('La Luz');
    expect((emitted[0] as { enrichment?: unknown }).enrichment).toBeDefined();
  });

  // Malformed body: drop it rather than throwing. A throw returns the message
  // to the queue, and a body that will never parse would cycle to the DLQ
  // three deliveries later having burned three enrichment attempts.
  it('drops an unparseable body without throwing', async () => {
    const emit = vi.fn(async () => {});
    await handleSQS({ Records: [{ eventSource: 'aws:sqs', body: 'not json' }] } as never, {
      enrich: async (m) => m,
      emit,
    });
    expect(emit).not.toHaveBeenCalled();
  });

  it('drops a body that is not a valid EventMessage', async () => {
    const emit = vi.fn(async () => {});
    await handleSQS(
      { Records: [{ eventSource: 'aws:sqs', body: JSON.stringify({ nope: 1 }) }] } as never,
      {
        enrich: async (m) => m,
        emit,
      },
    );
    expect(emit).not.toHaveBeenCalled();
  });

  // The outbound message is validated too: enrich() builds its result from
  // casts on external API responses, any of which can arrive as the wrong
  // type. An invalid enrichment block must not cost the event its ingestion —
  // it degrades to the plain, already-validated event instead.
  it('emits the plain event when the enriched output fails validation', async () => {
    const emitted: unknown[] = [];
    await handleSQS(sqsEvent as never, {
      enrich: async (m) => ({
        ...m,
        enrichment: {
          attempted_at: '2026-08-12T00:00:00Z',
          artist: { performer: 'La Luz', display_name: 'La Luz', status: 'ok' },
          tour: { status: 'ok', songs: [{ name: 'S', encore: 'not-a-number' }] }, // encore must be a number
        },
      }),
      emit: async (msgs) => {
        emitted.push(...msgs);
      },
    });

    expect(emitted).toHaveLength(1);
    const out = emitted[0] as { title: string; enrichment?: unknown };
    expect(out.title).toBe('La Luz');
    expect(out.enrichment).toBeUndefined();
  });

  // A send failure MUST throw: at batch_size 1 that returns the message to the
  // queue, which is the only retry mechanism this path has.
  it('propagates an emit failure', async () => {
    await expect(
      handleSQS(sqsEvent as never, {
        enrich: async (m) => m,
        emit: async () => {
          throw new Error('sqs down');
        },
      }),
    ).rejects.toThrow(/sqs down/);
  });
});

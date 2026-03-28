import { describe, expect, it } from 'vitest';

import { parseDebugPayload } from './trace-utils';

describe('parseDebugPayload', () => {
    it('prefers the unified top-level SSE envelope fields', () => {
        const parsed = parseDebugPayload(JSON.stringify({
            event: 'tool_call',
            createdAt: '2026-03-28T11:41:02.4602091Z',
            runId: 'run-123',
            stream: 'tool',
            data: {
                type: 'tool_call',
                text: 'payload text',
                toolName: 'browser',
            },
            agentEvent: {
                RunID: 'legacy-run',
                Stream: 'assistant',
                Data: {
                    type: 'text_delta',
                    text: 'legacy text',
                },
            },
        }));

        expect(parsed).toEqual({
            runId: 'run-123',
            stream: 'tool',
            type: 'tool_call',
            text: 'payload text',
            data: {
                type: 'tool_call',
                text: 'payload text',
                toolName: 'browser',
            },
            occurredAt: '2026-03-28T11:41:02.4602091Z',
        });
    });

    it('falls back to the legacy nested agentEvent envelope', () => {
        const parsed = parseDebugPayload(JSON.stringify({
            event: 'thinking',
            createdAt: '2026-03-28T11:41:01.1728048Z',
            agentEvent: {
                RunID: 'legacy-run',
                Stream: 'assistant',
                OccurredAt: '2026-03-28T11:41:01.1728048Z',
                Data: {
                    type: 'reasoning_delta',
                    text: 'legacy text',
                },
            },
        }));

        expect(parsed).toEqual({
            runId: 'legacy-run',
            stream: 'assistant',
            type: 'reasoning_delta',
            text: 'legacy text',
            data: {
                type: 'reasoning_delta',
                text: 'legacy text',
            },
            occurredAt: '2026-03-28T11:41:01.1728048Z',
        });
    });
});
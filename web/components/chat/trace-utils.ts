import type { TranslationKey } from '@/lib/i18n/translations';
import type { RunTrace, RunTraceStep, RunTraceMessage } from './types';
import { readString, readText, asRecord } from './utils';

// ---------------------------------------------------------------------------
// Pure trace-manipulation helpers – no React / rendering dependencies
// ---------------------------------------------------------------------------

export function mergeTrace(base: RunTrace | null, incoming: RunTrace | null): RunTrace | null {
    if (!base && !incoming) return null;
    if (!base) return incoming;
    if (!incoming) return base;

    const mergedSteps = [...base.steps];
    incoming.steps.forEach((step) => {
        const index = mergedSteps.findIndex((item) => item.key === step.key);
        if (index === -1) {
            mergedSteps.push(step);
            return;
        }
        mergedSteps[index] = {
            ...mergedSteps[index],
            ...step,
            content: step.content ?? mergedSteps[index].content,
            pendingContent: step.pendingContent ?? mergedSteps[index].pendingContent,
        };
    });

    // Merge finalized messages: keep whichever side has more entries.
    const mergedFinalizedMessages =
        incoming.finalizedMessages.length >= base.finalizedMessages.length
            ? incoming.finalizedMessages
            : base.finalizedMessages;

    return {
        ...base,
        runId: incoming.runId || base.runId,
        streamedText: incoming.streamedText || base.streamedText,
        pendingStreamedText: '',
        streamingLocked: incoming.streamingLocked || base.streamingLocked,
        finalizedMessages: mergedFinalizedMessages,
        steps: mergedSteps,
        nextStepOrdinal: Math.max(base.nextStepOrdinal, incoming.nextStepOrdinal),
        status:
            incoming.status === 'failed' || base.status === 'failed'
                ? 'failed'
                : incoming.status === 'completed'
                    ? 'completed'
                    : base.status,
        updatedAt: incoming.updatedAt || base.updatedAt,
    };
}

export function upsertTraceStep(trace: RunTrace, nextStep: RunTraceStep): RunTrace {
    const existingIndex = trace.steps.findIndex((step) => step.key === nextStep.key);
    if (existingIndex === -1) {
        return { ...trace, steps: [...trace.steps, nextStep] };
    }

    const existing = trace.steps[existingIndex];
    const steps = [...trace.steps];
    steps[existingIndex] = {
        ...existing,
        ...nextStep,
        content: nextStep.content ?? existing.content,
        pendingContent: nextStep.pendingContent ?? existing.pendingContent,
        reviewStatus: nextStep.reviewStatus ?? existing.reviewStatus,
        toolArgs: nextStep.toolArgs ?? existing.toolArgs,
    };
    return { ...trace, steps };
}

export function normalizeToolTraceStepKey(
    type: string,
    toolName: string,
    toolCallId: string,
): string {
    const normalizedType = type.trim().toLowerCase();
    const normalizedTool = toolName.trim() || 'tool';
    if (toolCallId) return toolCallId;

    switch (normalizedType) {
        case 'tool_execute_started':
        case 'tool_execute_completed':
        case 'tool_execute_failed':
            return `tool_exec:${normalizedTool}`;
        default:
            return normalizedType ? `${normalizedType}-${normalizedTool}` : normalizedTool;
    }
}

export function appendTraceStep(
    trace: RunTrace,
    step: Omit<RunTraceStep, 'key'> & { key?: string },
): RunTrace {
    const key = step.key || `step:${trace.nextStepOrdinal + 1}`;
    return {
        ...trace,
        nextStepOrdinal: trace.nextStepOrdinal + 1,
        steps: [...trace.steps, { ...step, key }],
    };
}

export function appendReasoningDelta(trace: RunTrace, text: string): RunTrace {
    if (!text) return trace;

    const lastStep = trace.steps[trace.steps.length - 1];
    const stepStatus: RunTraceStep['status'] =
        trace.status === 'failed' ? 'error' : trace.status === 'completed' ? 'success' : 'loading';

    if (lastStep?.kind === 'reasoning') {
        // Buffer incoming text; only flush completed lines (ending with \n) to content.
        const combined = `${lastStep.pendingContent ?? ''}${text}`;
        const lastNl = combined.lastIndexOf('\n');
        const nextContent =
            lastNl !== -1 ? `${lastStep.content ?? ''}${combined.slice(0, lastNl + 1)}` : (lastStep.content ?? '');
        const nextPending = lastNl !== -1 ? combined.slice(lastNl + 1) : combined;
        return upsertTraceStep(trace, {
            ...lastStep,
            content: nextContent,
            pendingContent: nextPending,
            status: stepStatus,
        });
    }
    // First reasoning delta — buffer without showing until a newline arrives.
    return appendTraceStep(trace, {
        kind: 'reasoning',
        title: 'Thinking',
        summary: 'Thinking',
        content: '',
        pendingContent: text,
        status: stepStatus,
    });
}

/**
 * Apply a single SSE debug event to a RunTrace, returning the updated trace.
 */
export function applyDebugEventToTrace(
    trace: RunTrace,
    stream: string,
    type: string,
    text: string,
    data: Record<string, unknown>,
    occurredAt: string,
    _t: (key: TranslationKey) => string,
): RunTrace {
    // ── Guard: ignore events after the run reached a terminal state ──
    // Post-completion the server may replay earlier events (text_delta,
    // reasoning_delta) from result.Events.  Dropping them here prevents
    // double-counting and streamedText corruption.
    if (trace.status === 'completed' || trace.status === 'failed') {
        return trace;
    }

    let next = trace;

    if (
        stream === 'assistant' &&
        (type === 'reasoning_delta' || readString(data.stream).toLowerCase() === 'thought')
    ) {
        next = appendReasoningDelta({ ...next, updatedAt: occurredAt }, text);
    } else if (stream === 'assistant' && type === 'reasoning_complete') {
        // Flush any buffered pendingContent in the current reasoning step.
        next = {
            ...next,
            updatedAt: occurredAt,
            steps: next.steps.map((step) =>
                step.kind === 'reasoning'
                    ? {
                        ...step,
                        content: `${step.content ?? ''}${step.pendingContent ?? ''}`,
                        pendingContent: '',
                        status: 'success' as const,
                    }
                    : step,
            ),
        };
    } else if (stream === 'assistant' && type === 'text_delta') {
        // Accumulate streaming text.  Replay-protection is handled by the
        // terminal-state guard at the top of this function.
        next = { ...next, streamedText: `${next.streamedText}${text}`, pendingStreamedText: text, updatedAt: occurredAt };
    } else if (stream === 'assistant' && (type === 'final' || type === 'yield')) {
        // Finalize the current message: push it to finalizedMessages and reset
        // streamedText so the next round of text_delta starts fresh.
        // 'yield' events carry the same structure as 'final' — the agent yielded
        // execution (e.g. waiting for sub-agents) and the server wrapped the
        // yield text as a streaming event before completing the run.
        const finalText = text || next.streamedText;
        const mediaUrl = readString(data.mediaUrl) || undefined;
        const mediaUrls = Array.isArray(data.mediaUrls)
            ? (data.mediaUrls as unknown[]).filter((u): u is string => typeof u === 'string' && u.length > 0)
            : undefined;
        const hasContent = Boolean(finalText) || Boolean(mediaUrl) || Boolean(mediaUrls && mediaUrls.length);
        const newFinalized: RunTraceMessage[] = hasContent
            ? [...next.finalizedMessages, {
                text: finalText,
                mediaUrl,
                mediaUrls: mediaUrls && mediaUrls.length > 0 ? mediaUrls : undefined,
            }]
            : next.finalizedMessages;
        next = {
            ...next,
            finalizedMessages: newFinalized,
            streamedText: '',
            pendingStreamedText: '',
            updatedAt: occurredAt,
            // Mark trace as yielded so lifecycle/run_completed does NOT
            // transition status → 'completed'.  This keeps the turn in
            // 'running' throughout the yield-resume cycle, preventing
            // the rendering path from oscillating between trace and history.
            ...(type === 'yield' ? { yielded: true } : {}),
        };
    } else if (stream === 'tool') {
        // A tool event means the assistant is calling a tool.  Reset any
        // leftover streamedText so the next round of text_delta events
        // starts a fresh message.
        if (next.streamedText || next.pendingStreamedText) {
            next = { ...next, streamedText: '', pendingStreamedText: '' };
        }

        const toolName = readString(data.toolName) || readString(data.name) || 'tool';
        const status = readString(data.status);
        const toolCallId = readString(data.toolCallId);
        const normalizedType = type.trim().toLowerCase();

        // ── Cerebellum review events ────────────────────────────────
        if (
            normalizedType === 'cerebellum_review_skipped' ||
            normalizedType === 'cerebellum_review_started' ||
            normalizedType === 'cerebellum_review_completed'
        ) {
            // Find the existing tool step by toolCallId, or create a temporary key
            const reviewStepKey = toolCallId || `tool_exec:${toolName.trim() || 'tool'}`;
            const existingStep = next.steps.find((step) => step.key === reviewStepKey);

            // Extract tool arguments from the event data (sent by the server)
            const rawArgs = data.args;
            const toolArgs: Record<string, unknown> | undefined =
                rawArgs && typeof rawArgs === 'object' && !Array.isArray(rawArgs)
                    ? (rawArgs as Record<string, unknown>)
                    : existingStep?.toolArgs;

            let reviewStatus: import('./types').CerebellumReviewStatus;
            if (normalizedType === 'cerebellum_review_skipped') {
                reviewStatus = { state: 'skipped', reason: readString(data.reason) };
            } else if (normalizedType === 'cerebellum_review_started') {
                reviewStatus = { state: 'reviewing' };
            } else {
                // cerebellum_review_completed
                const verdict = readString(data.verdict).toLowerCase();
                const reviewState = verdict === 'approve' ? 'approved'
                    : verdict === 'flag' ? 'flagged'
                        : verdict === 'reject' ? 'rejected'
                            : 'approved';
                reviewStatus = {
                    state: reviewState as 'approved' | 'flagged' | 'rejected',
                    reason: readString(data.reason),
                    risk: readString(data.risk),
                };
            }

            if (existingStep) {
                next = upsertTraceStep(next, { ...existingStep, reviewStatus, toolArgs });
            } else {
                // Pre-create the tool step so the review badge is visible before tool_execute_started
                next = appendTraceStep(next, {
                    key: reviewStepKey,
                    kind: 'tool',
                    title: toolName,
                    description: '',
                    summary: toolName,
                    status: 'loading',
                    reviewStatus,
                    toolArgs,
                });
            }
            return next;
        }

        if (
            (normalizedType === 'tool_execute_started' ||
                normalizedType === 'tool_execute_completed' ||
                normalizedType === 'tool_execute_failed') &&
            !toolCallId
        ) {
            return next;
        }

        const stepKey = normalizeToolTraceStepKey(type, toolName, toolCallId);
        const content =
            text ||
            (() => {
                const clone = { ...data };
                delete clone.type;
                return Object.keys(clone).length ? JSON.stringify(clone, null, 2) : '';
            })();

        let stepStatus: RunTraceStep['status'] = 'loading';
        if (normalizedType === 'tool_result' || normalizedType === 'tool_execute_completed') {
            stepStatus = 'success';
        } else if (normalizedType === 'tool_execute_failed') {
            stepStatus = 'error';
        }

        const existingStep = next.steps.find((step) => step.key === stepKey);

        // Extract tool arguments from tool_execute_started event
        const rawArgs = data.args;
        const toolArgs: Record<string, unknown> | undefined =
            rawArgs && typeof rawArgs === 'object' && !Array.isArray(rawArgs)
                ? (rawArgs as Record<string, unknown>)
                : existingStep?.toolArgs;

        const stepPayload: RunTraceStep = {
            key: stepKey,
            kind: 'tool',
            title: toolName,
            description: status || type,
            summary: `${toolName}${status ? ` · ${status}` : ''}`,
            status: stepStatus,
            content: typeof content === 'string' ? content : '',
            // Preserve existing reviewStatus when updating a step
            reviewStatus: existingStep?.reviewStatus,
            toolArgs,
        };

        if (existingStep) {
            next = upsertTraceStep(next, stepPayload);
        } else {
            next = appendTraceStep(next, stepPayload);
        }
    } else if (stream === 'lifecycle') {
        const lifecycleType = type || 'lifecycle';
        if (lifecycleType.includes('failed') || lifecycleType === 'error') {
            next = { ...next, status: 'failed', updatedAt: occurredAt };
            next.steps.forEach((step) => {
                if (step.kind === 'reasoning') {
                    next = upsertTraceStep(next, { ...step, status: 'error' });
                }
            });
        }
        if (lifecycleType.includes('completed') || lifecycleType === 'done') {
            next = { ...next, status: 'completed', updatedAt: occurredAt };
            // Flush any un-finalized streamedText as a last message.
            if (next.streamedText.trim()) {
                next = {
                    ...next,
                    finalizedMessages: [
                        ...next.finalizedMessages,
                        { text: next.streamedText },
                    ],
                    streamedText: '',
                    pendingStreamedText: '',
                };
            }
            // Flush any buffered pendingContent in reasoning steps on completion.
            next = {
                ...next,
                steps: next.steps.map((step) =>
                    step.kind === 'reasoning'
                        ? {
                            ...step,
                            content: `${step.content ?? ''}${step.pendingContent ?? ''}`,
                            pendingContent: '',
                            status: 'success' as const,
                        }
                        : step,
                ),
            };
        }
    } else if (stream === 'delivery') {
        // Delivery events (queued, sending, sent, etc.) — currently a no-op
        // in trace rendering but acknowledged here so they don't fall through.
    }

    return next;
}

/** Create a blank RunTrace with sensible defaults. */
export function createEmptyTrace(runId: string): RunTrace {
    return {
        runId,
        streamedText: '',
        pendingStreamedText: '',
        finalizedMessages: [],
        steps: [],
        nextStepOrdinal: 0,
        status: 'running',
    };
}

/**
 * Parse a raw SSE debug payload into its constituent fields.
 * Returns `null` when the event cannot be meaningfully processed.
 */
export function parseDebugPayload(raw: string) {
    try {
        const payload = JSON.parse(raw) as Record<string, unknown>;
        const envelope = asRecord(payload);
        const directData = asRecord(envelope.data ?? envelope.Data);
        const directRunId = readString(envelope.runId ?? envelope.RunID ?? envelope.runID);
        const directStream = readString(envelope.stream ?? envelope.Stream).toLowerCase();
        const directOccurredAt = readString(envelope.occurredAt ?? envelope.OccurredAt ?? envelope.createdAt ?? envelope.CreatedAt);

        if (directStream || directRunId || Object.keys(directData).length > 0) {
            const type = readString(directData.type).toLowerCase();
            const text = readText(directData.text);
            return {
                runId: directRunId,
                stream: directStream,
                type,
                text,
                data: directData,
                occurredAt: directOccurredAt,
            };
        }

        const agentEvent = asRecord(envelope.agentEvent ?? envelope.AgentEvent);
        const data = asRecord(agentEvent.Data ?? agentEvent.data);
        const runId = readString(
            agentEvent.RunID ?? agentEvent.runID ?? agentEvent.runId,
        );
        const stream = readString(agentEvent.Stream ?? agentEvent.stream).toLowerCase();
        const type = readString(data.type).toLowerCase();
        const text = readText(data.text);
        const occurredAt = readString(agentEvent.OccurredAt ?? agentEvent.occurredAt);
        return { runId, stream, type, text, data, occurredAt };
    } catch {
        return null;
    }
}

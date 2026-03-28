'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import { Bubble, FileCard, Sender } from '@ant-design/x';
import { Dropdown, Select } from 'antd';
import { Loader2, MessageSquare, Play, Plus, Search, ListChecks } from 'lucide-react';

import { useI18n } from '@/lib/i18n/I18nContext';
import {
  APIClientError,
  apiGet,
  apiPost,
  apiURL,
  type ChatCancelResponse,
  type ChatHistoryResponse,
  type ChatSendResponse,
} from '@/lib/api';
import {
  DEFAULT_SESSION_KEY,
  DEFAULT_CHANNEL,
  DEFAULT_TO,
  HISTORY_PAGE_SIZE,
} from '@/lib/constants';

import type { RunTrace, LocalTurn } from './types';
import { readString, toChatAttachmentPayload } from './utils';
import {
  mergeTrace,
  applyDebugEventToTrace,
  createEmptyTrace,
  parseDebugPayload,
} from './trace-utils';
import { toBubbleItems, BUBBLE_ROLES } from './renderers';
import { useThemeSync, useAttachments, useScrollAnchor } from './hooks';

function getPayloadFallbackText(payloads: ChatSendResponse['payloads'] | undefined): string {
  if (!Array.isArray(payloads)) return '';
  for (let index = payloads.length - 1; index >= 0; index -= 1) {
    const text = readString(payloads[index]?.text).trim();
    if (text) return text;
  }
  return '';
}

function getPrimaryRunId(runId: string): string {
  const normalized = runId.trim();
  if (!normalized) return '';
  const separatorIndex = normalized.indexOf(':');
  return separatorIndex === -1 ? normalized : normalized.slice(0, separatorIndex);
}

function isDerivedRunId(runId: string): boolean {
  const normalized = runId.trim();
  if (!normalized) return false;
  return getPrimaryRunId(normalized) !== normalized;
}

// ---------------------------------------------------------------------------
// ChatView component
// ---------------------------------------------------------------------------

export function ChatView() {
  const { t } = useI18n();
  const router = useRouter();
  const isDark = useThemeSync();

  // --- Session key (selectable) -------------------------------------------
  const [sessionKey, setSessionKey] = useState(DEFAULT_SESSION_KEY);
  const [sessionKeyOptions, setSessionKeyOptions] = useState<{ value: string; label: string }[]>(
    [{ value: DEFAULT_SESSION_KEY, label: DEFAULT_SESSION_KEY }],
  );

  // Fetch server-configured default session key from bootstrap
  useEffect(() => {
    apiGet<{ sessionKey: string }>('/api/workspace/chat/bootstrap')
      .then((data) => {
        if (data.sessionKey && data.sessionKey !== DEFAULT_SESSION_KEY) {
          const serverKey = data.sessionKey;
          setSessionKey((current) => (current === DEFAULT_SESSION_KEY ? serverKey : current));
          setSessionKeyOptions((prev) => {
            const keys = new Set(prev.map((o) => o.value));
            if (!keys.has(serverKey)) {
              return [{ value: serverKey, label: serverKey }, ...prev];
            }
            return prev;
          });
        }
      })
      .catch(() => undefined);
  }, []);

  const {
    attachments,
    setAttachments,
    appendFiles,
    removeAttachment,
    clearAttachments,
    attachmentItems,
    fileInputRef,
    imageInputRef,
  } = useAttachments();

  // --- Core chat state -----------------------------------------------------
  const [history, setHistory] = useState<ChatHistoryResponse | null>(null);
  const [historyLoading, setHistoryLoading] = useState(true);
  const [loadingMoreHistory, setLoadingMoreHistory] = useState(false);
  const [hasMoreHistory, setHasMoreHistory] = useState(true);
  const [nextHistoryBefore, setNextHistoryBefore] = useState(0);
  const [loadMoreError, setLoadMoreError] = useState('');
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const [composerResetKey, setComposerResetKey] = useState(0);
  const [error, setError] = useState('');
  const [errorCode, setErrorCode] = useState('');
  const [traces, setTraces] = useState<Record<string, RunTrace>>({});
  const [activeRunId, setActiveRunId] = useState('');
  // The real backend runId extracted from SSE events (may differ from
  // activeRunId which can hold a frontend pending-xxx key while ChatSend
  // is still in-flight).
  const realRunIdRef = useRef('');
  // Tracks the runId of the most recent yield so that the next resumed run
  // can be linked to the same LocalTurn (yield-resume continuation).
  const lastYieldedRunIdRef = useRef('');
  const [localTurns, setLocalTurns] = useState<LocalTurn[]>([]);

  // --- Refs ----------------------------------------------------------------
  const loadedHistoryCountRef = useRef(HISTORY_PAGE_SIZE);
  const activeRunIdRef = useRef('');
  const localTurnsRef = useRef<LocalTurn[]>([]);
  const pendingTraceKeyRef = useRef('');
  const prependScrollRestoreRef = useRef<{ top: number; height: number } | null>(null);
  // Set to true after adding a local turn; cleared once the scroll fires.
  const scrollToLastUserBubblePendingRef = useRef(false);

  // --- Derived state -------------------------------------------------------
  const activeTrace = activeRunId ? traces[activeRunId] || null : null;
  const canCancel = sending || activeTrace?.status === 'running';
  const bubbleItems = useMemo(
    () => toBubbleItems(history, localTurns, traces, isDark, t),
    [history, isDark, localTurns, t, traces],
  );

  // True only while a user-initiated turn is in-flight (pending → running).
  // Controls the bottom spacer that lets the user bubble scroll to the viewport top.
  const showBottomSpacer = sending || localTurns.some(
    (turn) => turn.status === 'pending' || turn.status === 'running',
  );

  // --- Scroll anchoring ----------------------------------------------------
  const {
    messagesViewportRef,
    historyTopSentinelRef,
    contentContainerRef,
    scrollToLastUserBubble,
    scrollToLastUserBubbleWhenStable,
  } = useScrollAnchor(bubbleItems);

  // --- Keep refs in sync ---------------------------------------------------
  useEffect(() => {
    loadedHistoryCountRef.current =
      Array.isArray(history?.messages) && history.messages.length > 0
        ? history.messages.length
        : HISTORY_PAGE_SIZE;
  }, [history]);

  useEffect(() => {
    activeRunIdRef.current = activeRunId;
  }, [activeRunId]);

  useEffect(() => {
    localTurnsRef.current = localTurns;
  }, [localTurns]);

  // --- Initial history load ------------------------------------------------
  useEffect(() => {
    let cancelled = false;
    setHistoryLoading(true);
    setLoadingMoreHistory(false);
    setHasMoreHistory(true);
    setNextHistoryBefore(0);

    apiGet<ChatHistoryResponse>(
      `/api/workspace/chat/history?sessionKey=${encodeURIComponent(sessionKey)}&limit=${HISTORY_PAGE_SIZE}`,
    )
      .then((next) => {
        if (cancelled) return;
        // Set the scroll-pending flag BEFORE setHistory so that when bubbleItems
        // recomputes and useLayoutEffect([bubbleItems]) fires, the scroll runs
        // against the already-committed DOM — no rAF gap, no flash.
        // StrictMode safety: cancelled = true on the first mount's cleanup, so
        // only the second (real) mount's .then() reaches here.
        scrollToLastUserBubblePendingRef.current = true;
        setHistory(next);
        setHasMoreHistory(Boolean(next.hasMore));
        setNextHistoryBefore(next.nextBefore || 0);
        // Batch with the state updates above so the skeleton and bubbles swap
        // in a single render — avoids a flash of the empty/loading state.
        if (!cancelled) setHistoryLoading(false);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : t('chat.loadError'));
          setErrorCode('');
          setHistoryLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionKey]);


  // --- SSE event stream ----------------------------------------------------
  useEffect(() => {
    const source = new EventSource(
      apiURL(
        `/api/workspace/chat/events?sessionKey=${encodeURIComponent(sessionKey)}`,
      ),
    );

    const refreshHistory = () => {
      // Always refresh history on SSE message events so that messages
      // pushed from external sources (e.g. another client, a scheduled
      // task) are displayed even when there is an active run.
      apiGet<ChatHistoryResponse>(
        `/api/workspace/chat/history?sessionKey=${encodeURIComponent(sessionKey)}&limit=${loadedHistoryCountRef.current}`,
      )
        .then((next) => {
          setHistory(next);
          setHasMoreHistory(Boolean(next.hasMore));
          setNextHistoryBefore(next.nextBefore || 0);
        })
        .catch(() => undefined);
    };

    const handleDebug = (event: MessageEvent<string>) => {
      const parsed = parseDebugPayload(event.data);
      if (!parsed) return;

      const eventRunId = parsed.runId.trim();
      const primaryRunId = getPrimaryRunId(eventRunId);
      const derivedRun = eventRunId ? isDerivedRunId(eventRunId) : false;
      const pendingKey = pendingTraceKeyRef.current;
      const hasPrimaryTurn = primaryRunId
        ? localTurnsRef.current.some((turn) => turn.runIds.includes(primaryRunId))
        : false;

      // Ignore non-chat debug streams such as memory_flush / compaction.
      if (!['assistant', 'tool', 'lifecycle', 'delivery'].includes(parsed.stream)) {
        return;
      }

      // Track the primary backend runId for cancel. Derived runs such as
      // :memory-flush or :image must not replace the visible turn's runId.
      if (primaryRunId && !primaryRunId.startsWith('pending-') && (!derivedRun || pendingKey || hasPrimaryTurn)) {
        realRunIdRef.current = primaryRunId;
      }

      // ── Pending → Real migration (one-time) ──────────────────
      // First SSE event that carries the real backend runId while we
      // still have a pending trace:  migrate the pending trace to the
      // real key so all subsequent events and rendering use one key.
      if (pendingKey && primaryRunId && !primaryRunId.startsWith('pending-')) {
        pendingTraceKeyRef.current = '';
        activeRunIdRef.current = primaryRunId;
        setActiveRunId(primaryRunId);

        setLocalTurns((current) =>
          current.map((turn) =>
            turn.runIds.includes(pendingKey)
              ? { ...turn, runIds: turn.runIds.map(id => id === pendingKey ? primaryRunId : id), status: 'running' }
              : turn,
          ),
        );

        setTraces((prev) => {
          const next = { ...prev };
          const pending = next[pendingKey];
          delete next[pendingKey];
          const existing = next[primaryRunId];
          const base = pending
            ? (existing
              ? mergeTrace(existing, { ...pending, runId: primaryRunId }) || { ...pending, runId: primaryRunId }
              : { ...pending, runId: primaryRunId })
            : (existing || createEmptyTrace(primaryRunId));
          next[primaryRunId] = applyDebugEventToTrace(
            base,
            parsed.stream,
            parsed.type,
            parsed.text,
            parsed.data,
            parsed.occurredAt,
            t,
          );
          return next;
        });

        // Track yield even during pending→real migration.
        if (parsed.stream === 'assistant' && parsed.type === 'yield') {
          lastYieldedRunIdRef.current = primaryRunId;
        }

        // Derived child/internal runs belong to the primary run and should
        // not render their own assistant bubble.
        if (derivedRun) return;
        return;
      }

      // Child/internal runs such as :memory-flush or :image can emit their
      // own assistant/tool/lifecycle events on the same session. They should
      // not create a separate visible turn in the chat UI.
      if (derivedRun && (hasPrimaryTurn || activeRunIdRef.current === primaryRunId || realRunIdRef.current === primaryRunId)) {
        return;
      }

      // ── Yield-resume continuation detection ─────────────────
      // When a yielded run completes and the server resumes with a new
      // RunID, ADD it to the existing LocalTurn's runIds (not replace).
      // Each run keeps its own trace — no carry-forward needed.
      if (eventRunId && !pendingKey) {
        const yieldedFromRunId = lastYieldedRunIdRef.current;
        if (yieldedFromRunId && yieldedFromRunId !== eventRunId) {
          lastYieldedRunIdRef.current = '';
          activeRunIdRef.current = eventRunId;
          setActiveRunId(eventRunId);

          // Add new runId to existing turn's runIds array.
          setLocalTurns((current) => {
            if (current.some((turn) => turn.runIds.includes(eventRunId))) return current;
            return current.map((turn) =>
              turn.runIds.includes(yieldedFromRunId)
                ? { ...turn, runIds: [...turn.runIds, eventRunId], status: 'running' as const }
                : turn,
            );
          });

          // Create fresh trace for the new run — NO carry-forward.
          setTraces((prev) => ({
            ...prev,
            [eventRunId]: applyDebugEventToTrace(
              prev[eventRunId] || createEmptyTrace(eventRunId),
              parsed.stream,
              parsed.type,
              parsed.text,
              parsed.data,
              parsed.occurredAt,
              t,
            ),
          }));

          // Track yield if THIS event is itself a yield.
          if (parsed.stream === 'assistant' && parsed.type === 'yield') {
            lastYieldedRunIdRef.current = eventRunId;
          }
          return;
        }
      }

      // ── Standard routing ─────────────────────────────────────
      // Primary: event's own runId.
      // Fallback: pending key (send in-flight, no runId in event yet).
      // Last resort: active run ref.
      const traceKey = eventRunId || pendingKey || activeRunIdRef.current;
      if (!traceKey) return;

      setTraces((prev) => ({
        ...prev,
        [traceKey]: applyDebugEventToTrace(
          prev[traceKey] || createEmptyTrace(traceKey),
          parsed.stream,
          parsed.type,
          parsed.text,
          parsed.data,
          parsed.occurredAt,
          t,
        ),
      }));

      // ── Auto-create a LocalTurn for orphan runs ──────────────
      // SSE events for a runId not associated with any local turn
      // (e.g. background task, external trigger) need a placeholder
      // turn so the assistant bubbles render in real-time.
      if (eventRunId && !pendingKey) {
        setLocalTurns((current) => {
          if (current.some((turn) => turn.runIds.includes(eventRunId))) return current;
          return [
            ...current,
            {
              id: `sse-${eventRunId}`,
              userText: '',
              createdAt: parsed.occurredAt || new Date().toISOString(),
              runIds: [eventRunId],
              status: 'running' as const,
            },
          ];
        });
      }

      // ── Track yield for continuation detection ───────────────
      if (parsed.stream === 'assistant' && parsed.type === 'yield') {
        lastYieldedRunIdRef.current = eventRunId || traceKey;
      }
    };

    source.addEventListener('message', refreshHistory);
    // Listen for all typed SSE agent event channels.
    // Each uses the same handler — the payload always contains the full AgentEvent.
    const agentEventTypes = [
      'thinking',           // 思考流 — reasoning deltas
      'thinking_complete',  // 思考结果 — reasoning done
      'streaming',          // 消息流 — text deltas
      'message_complete',   // 消息结果 — finalized assistant message
      'tool_call',          // 工具调用 — tool invocation events
      'delivery',           // 投递事件 — message delivery events
      'lifecycle',          // 生命周期 — lifecycle / system events
      'debug',              // backward compat — legacy catch-all
    ] as const;
    for (const eventType of agentEventTypes) {
      source.addEventListener(eventType, handleDebug as EventListener);
    }
    source.onerror = () => source.close();
    return () => {
      source.removeEventListener('message', refreshHistory);
      for (const eventType of agentEventTypes) {
        source.removeEventListener(eventType, handleDebug as EventListener);
      }
      source.close();
    };
  }, [t, sessionKey]);

  // --- Sync trace status → local turn status -------------------------------
  useEffect(() => {
    setLocalTurns((current) =>
      current.map((turn) => {
        if (turn.runIds.length === 0) return turn;
        const latestRunId = turn.runIds[turn.runIds.length - 1];
        const trace = traces[latestRunId];
        if (!trace) return turn;
        if (trace.status === 'failed' && turn.status !== 'failed')
          return { ...turn, status: 'failed' };
        // Don't mark completed if the trace was yielded (another run will follow).
        if (trace.status === 'completed' && !trace.yielded && turn.status === 'running')
          return { ...turn, status: 'completed' };
        return turn;
      }),
    );

    // Clear activeRunId when the active trace reaches a terminal state
    // AND is not yielded (yielded traces will be followed by a resumed run).
    const currentActiveRunId = activeRunIdRef.current;
    if (currentActiveRunId) {
      const trace = traces[currentActiveRunId];
      if (trace && (trace.status === 'completed' || trace.status === 'failed') && !trace.yielded) {
        activeRunIdRef.current = '';
        setActiveRunId('');
      }
    }
  }, [traces]);

  // --- Scroll to last user bubble (initial load + after send) ---
  // Runs once when the pending flag is set (initial load / send), then clears.
  // During SSE streaming the flag is false so the effect is a no-op.
  // No ResizeObserver — only the user controls scrolling during streaming.
  useEffect(() => {
    if (!scrollToLastUserBubblePendingRef.current) return;
    scrollToLastUserBubblePendingRef.current = false;
    scrollToLastUserBubbleWhenStable();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bubbleItems]);

  // --- Send ----------------------------------------------------------------
  const handleSend = async () => {
    if ((!input.trim() && attachments.length === 0) || sending) return;

    const text = input.trim();
    const composerAttachments = attachments;
    const hasComposerAttachments = composerAttachments.length > 0;
    const localTurnId = `turn-${Date.now()}`;
    const pendingKey = `pending-${localTurnId}`;

    setInput('');
    if (hasComposerAttachments) {
      clearAttachments();
      setComposerResetKey((current) => current + 1);
    }
    setSending(true);
    setError('');
    setErrorCode('');
    pendingTraceKeyRef.current = pendingKey;
    activeRunIdRef.current = pendingKey;
    realRunIdRef.current = '';
    lastYieldedRunIdRef.current = '';
    setActiveRunId(pendingKey);

    setLocalTurns((current) => [
      ...current,
      {
        id: localTurnId,
        userText: text,
        createdAt: new Date().toISOString(),
        runIds: [pendingKey],
        status: 'pending',
        attachments: composerAttachments.map((a) => ({
          id: a.id,
          name: a.name,
          kind: a.kind,
          previewUrl: a.previewUrl,
        })),
      },
    ]);
    // Flag scroll – fires in the useEffect below after the new bubble is in the DOM.
    scrollToLastUserBubblePendingRef.current = true;
    setTraces((prev) => ({
      ...prev,
      [pendingKey]: prev[pendingKey] || createEmptyTrace(pendingKey),
    }));

    try {
      const attachmentPayloads = await Promise.all(composerAttachments.map((attachment) => toChatAttachmentPayload(attachment)));
      const response = await apiPost<ChatSendResponse>(
        '/api/workspace/chat/send',
        {
          sessionKey: sessionKey,
          channel: DEFAULT_CHANNEL,
          to: DEFAULT_TO,
          message: text,
          attachments: attachmentPayloads,
        },
      );
      // Optionally update history from the API response (fast path).
      // With fire-and-forget APIs this may be empty; SSE refreshHistory will fill it.
      if (Array.isArray(response.messages) && response.messages.length > 0) {
        setHistory({
          sessionKey: response.sessionKey || sessionKey,
          sessionId: response.sessionId,
          skillsSnapshot: response.skillsSnapshot,
          messages: response.messages,
        });
      }

      // Map pending key → real runId (the ONLY essential info from the response)
      const payloadFallbackText = getPayloadFallbackText(response.payloads);
      pendingTraceKeyRef.current = '';
      activeRunIdRef.current = response.runId;
      setActiveRunId(response.runId);

      // Turn goes to 'running'; completion is detected by the trace sync effect
      // when SSE lifecycle/completed arrives (or trace.status becomes 'completed').
      setLocalTurns((current) =>
        current.map((turn) =>
          turn.id === localTurnId
            ? { ...turn, runIds: turn.runIds.map(id => id === pendingKey ? response.runId : id), status: 'running' }
            : turn,
        ),
      );

      setTraces((prev) => {
        const pendingTrace = prev[pendingKey] || null;
        const existingTrace = prev[response.runId] || null;
        const merged =
          mergeTrace(
            existingTrace || createEmptyTrace(response.runId),
            pendingTrace,
          ) || createEmptyTrace(response.runId);
        const nextTrace: RunTrace = {
          ...merged,
          runId: response.runId,
        };
        // If the API returned synchronously with payload text but SSE didn't
        // deliver a final event, push the text as a finalized message.
        if (!response.queued && payloadFallbackText && nextTrace.finalizedMessages.length === 0) {
          nextTrace.finalizedMessages = [{ text: payloadFallbackText }];
          nextTrace.streamedText = '';
          nextTrace.pendingStreamedText = '';
          nextTrace.status = 'completed';
        }
        const next = {
          ...prev,
          [response.runId]: nextTrace,
        };
        // If pending trace was already migrated by handleDebug, the key
        // might not exist — delete is a safe no-op in that case.
        delete next[pendingKey];
        return next;
      });
      // History is NOT refreshed here — the final assistant text is accumulated
      // in trace.streamedText via SSE text_delta events and displayed directly
      // by toBubbleItems / renderAssistantTurnContent without a history round-trip.
    } catch (err) {
      pendingTraceKeyRef.current = '';
      if (err instanceof APIClientError && err.code === 'NO_DEFAULT_MODEL') {
        setError(t('chat.noDefaultModelConfigured'));
        setErrorCode(err.code);
      } else {
        setError(err instanceof Error ? err.message : t('chat.sendError'));
        setErrorCode('');
      }
      setInput(text);
      if (hasComposerAttachments) {
        setAttachments(composerAttachments);
        setComposerResetKey((current) => current + 1);
      }
      setLocalTurns((current) =>
        current.filter((turn) => turn.id !== localTurnId),
      );
      setTraces((prev) => {
        const next = { ...prev };
        delete next[pendingKey];
        return next;
      });
    } finally {
      setSending(false);
    }
  };

  // --- Cancel --------------------------------------------------------------
  const handleCancel = async () => {
    if (!canCancel) return;
    setError('');
    setErrorCode('');
    activeRunIdRef.current = '';
    try {
      // Prefer the real backend runId from SSE events; fall back to the
      // state value. If neither is a real id, omit runId so the backend
      // cancels the entire session.
      const cancelRunId = realRunIdRef.current || activeRunId || undefined;
      const response = await apiPost<ChatCancelResponse>(
        '/api/workspace/chat/cancel',
        { sessionKey: sessionKey, runId: cancelRunId && !cancelRunId.startsWith('pending-') ? cancelRunId : undefined },
      );
      if (Array.isArray(response.messages)) {
        const nextCount = response.messages.length || 0;
        setHistory({
          sessionKey: response.sessionKey || sessionKey,
          sessionId: history?.sessionId,
          skillsSnapshot: response.skillsSnapshot,
          messages: response.messages,
        });
        setHasMoreHistory(nextCount >= HISTORY_PAGE_SIZE);
        setNextHistoryBefore(Math.max(0, nextCount - HISTORY_PAGE_SIZE));
      }
      if (activeRunId) {
        setTraces((prev) => ({
          ...prev,
          [activeRunId]: prev[activeRunId]
            ? { ...prev[activeRunId], status: 'completed' }
            : prev[activeRunId],
        }));
        setLocalTurns((current) =>
          current.map((turn) =>
            turn.runIds.includes(activeRunId) ? { ...turn, status: 'canceled' } : turn,
          ),
        );
      }
      setSending(false);
    } catch (err) {
      setErrorCode('');
      setError(
        err instanceof Error ? err.message : t('chat.cancelError'),
      );
    }
  };

  // --- Load older history (infinite scroll) --------------------------------
  const loadOlderHistory = useCallback(async () => {
    const viewport = messagesViewportRef.current;
    if (!viewport || historyLoading || loadingMoreHistory || !hasMoreHistory || loadMoreError) return;

    prependScrollRestoreRef.current = {
      top: viewport.scrollTop,
      height: viewport.scrollHeight,
    };
    setLoadingMoreHistory(true);
    setLoadMoreError('');

    try {
      const next = await apiGet<ChatHistoryResponse>(
        `/api/workspace/chat/history?sessionKey=${encodeURIComponent(sessionKey)}&limit=${HISTORY_PAGE_SIZE}&before=${nextHistoryBefore}`,
      );
      const olderMessages = Array.isArray(next.messages) ? next.messages : [];
      if (!olderMessages.length) {
        prependScrollRestoreRef.current = null;
        setHasMoreHistory(false);
      } else {
        setHistory((current) => ({
          sessionKey: next.sessionKey || current?.sessionKey || sessionKey,
          sessionId: next.sessionId || current?.sessionId,
          skillsSnapshot: next.skillsSnapshot || current?.skillsSnapshot,
          messages: [...olderMessages, ...(current?.messages || [])],
          total: next.total,
          hasMore: next.hasMore,
          nextBefore: next.nextBefore,
        }));
        setHasMoreHistory(Boolean(next.hasMore));
        setNextHistoryBefore(next.nextBefore || 0);
      }
    } catch (err) {
      setErrorCode('');
      prependScrollRestoreRef.current = null;
      setLoadMoreError(
        err instanceof Error ? err.message : t('chat.loadMoreError'),
      );
    } finally {
      setLoadingMoreHistory(false);
    }
  }, [hasMoreHistory, historyLoading, loadingMoreHistory, messagesViewportRef, nextHistoryBefore, loadMoreError, sessionKey, t]);

  // --- IntersectionObserver for infinite scroll ----------------------------
  useEffect(() => {
    const viewport = messagesViewportRef.current;
    const sentinel = historyTopSentinelRef.current;
    if (!viewport || !sentinel) return;

    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (!entry?.isIntersecting) return;
        void loadOlderHistory();
      },
      { root: viewport, rootMargin: '80px 0px 0px 0px', threshold: 0 },
    );
    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [loadOlderHistory, messagesViewportRef, historyTopSentinelRef]);

  // --- Render --------------------------------------------------------------
  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden bg-zinc-50 text-zinc-900 transition-colors dark:bg-zinc-950 dark:text-zinc-100">
      {/* Header */}
      <div className="border-b border-zinc-200 bg-white px-6 py-4 dark:border-zinc-800 dark:bg-zinc-950">
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0 flex-1">
            <h2 className="text-xl font-semibold tracking-tight text-zinc-900 dark:text-zinc-100">
              {t('sidebar.chat')}
            </h2>
            <div className="mt-1.5 flex items-center gap-2">
              <Select
                size="small"
                value={sessionKey}
                options={sessionKeyOptions}
                showSearch
                allowClear={false}
                filterOption={false}
                onSearch={(val) => {
                  // Allow typing a custom key - keep it in the options list
                  if (!val) return;
                  setSessionKeyOptions((prev) => {
                    const keys = new Set(prev.map((o) => o.value));
                    if (keys.has(val)) return prev;
                    return [{ value: val, label: val }, ...prev.filter((o) => o.value !== val)];
                  });
                }}
                onChange={(val: string) => {
                  if (val && val !== sessionKey) {
                    setSessionKey(val);
                    setHistory(null);
                    setLocalTurns([]);
                    setTraces({});
                    setActiveRunId('');
                  }
                }}
                style={{ minWidth: 200, maxWidth: 360 }}
                className="font-mono text-xs"
                variant="borderless"
                styles={{ popup: { root: { minWidth: 280, fontFamily: 'monospace', fontSize: 12 } } }}
              />
            </div>

          </div>

        </div>
      </div>

      {/* Messages viewport */}
      <div
        ref={messagesViewportRef}
        className="relative flex-1 overflow-y-auto overscroll-contain bg-white px-4 py-5 [overscroll-behavior-y:contain] dark:bg-zinc-950 md:px-6"
      >
        <div ref={contentContainerRef} className="mx-auto flex max-w-4xl flex-col gap-4">
          <div
            ref={historyTopSentinelRef}
            className="h-px w-full shrink-0"
            aria-hidden="true"
          />

          {loadingMoreHistory ? (
            <div className="flex items-center justify-center py-1 text-xs text-zinc-500 dark:text-zinc-400">
              <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />
              <span>{t('chat.loadingMore')}</span>
            </div>
          ) : null}

          {loadMoreError ? (
            <div className="flex items-center justify-center gap-2 py-2 text-xs text-rose-600 dark:text-rose-400">
              <span>{loadMoreError}</span>
              <button
                onClick={() => {
                  setLoadMoreError('');
                  void loadOlderHistory();
                }}
                className="rounded px-2 py-0.5 text-xs font-medium text-rose-600 hover:bg-rose-100 dark:text-rose-400 dark:hover:bg-rose-900/30"
              >
                {t('common.retry')}
              </button>
            </div>
          ) : null}

          {!bubbleItems.length ? (
            <div className="flex flex-col items-center pt-8 pb-4">
              {/* Greeting */}
              <img src="/logo.svg" alt="logo" className="mb-2 h-14 w-14" />
              <h2 className="mt-4 text-xl font-semibold text-zinc-900 dark:text-zinc-100">
                {t('chat.emptyTitle')}
              </h2>
              <p className="mt-1.5 text-sm text-zinc-500 dark:text-zinc-400">
                {t('chat.emptyDesc')}
              </p>

              {/* Quick-start suggestion cards */}
              <div className="mt-8 grid w-full max-w-lg grid-cols-2 gap-3">
                {([
                  { icon: MessageSquare, title: t('chat.emptyHintChat'), desc: t('chat.emptyHintChatDesc'), color: 'text-sky-600 dark:text-sky-400', bg: 'bg-sky-50 dark:bg-sky-950/40' },
                  { icon: Play, title: t('chat.emptyHintExecute'), desc: t('chat.emptyHintExecuteDesc'), color: 'text-emerald-600 dark:text-emerald-400', bg: 'bg-emerald-50 dark:bg-emerald-950/40' },
                  { icon: ListChecks, title: t('chat.emptyHintPlan'), desc: t('chat.emptyHintPlanDesc'), color: 'text-amber-600 dark:text-amber-400', bg: 'bg-amber-50 dark:bg-amber-950/40' },
                  { icon: Search, title: t('chat.emptyHintResearch'), desc: t('chat.emptyHintResearchDesc'), color: 'text-violet-600 dark:text-violet-400', bg: 'bg-violet-50 dark:bg-violet-950/40' },
                ] as const).map(({ icon: Icon, title, desc, color, bg }) => (
                  <button
                    key={title}
                    type="button"
                    onClick={() => setInput(desc)}
                    className="group flex flex-col gap-1.5 rounded-xl border border-zinc-200 bg-white p-4 text-left transition-all hover:border-zinc-300 hover:shadow-md dark:border-zinc-800 dark:bg-zinc-900 dark:hover:border-zinc-700"
                  >
                    <div className={`flex h-8 w-8 items-center justify-center rounded-lg ${bg}`}>
                      <Icon className={`h-4 w-4 ${color}`} />
                    </div>
                    <span className="text-sm font-medium text-zinc-900 dark:text-zinc-100">{title}</span>
                    <span className="text-xs leading-relaxed text-zinc-500 dark:text-zinc-400">{desc}</span>
                  </button>
                ))}
              </div>
            </div>
          ) : (
            <Bubble.List
              autoScroll={false}
              items={bubbleItems}
              role={BUBBLE_ROLES}
            />
          )}

          {error ? (
            <div className="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700 dark:border-rose-900/50 dark:bg-rose-950/30 dark:text-rose-300">
              <div>{error}</div>
              {errorCode === 'NO_DEFAULT_MODEL' ? (
                <button
                  type="button"
                  onClick={() => router.push('/brain')}
                  className="mt-3 inline-flex rounded-md border border-rose-300 bg-white px-3 py-1.5 text-sm font-medium text-rose-700 transition-colors hover:bg-rose-100 dark:border-rose-800 dark:bg-transparent dark:text-rose-300 dark:hover:bg-rose-900/40"
                >
                  {t('chat.configureModel')}
                </button>
              ) : null}
            </div>
          ) : null}

          {/* Spacer: only present while a user turn is in-flight (pending → running).
               Guarantees enough height to scroll the user bubble to the viewport top.
               Removed once the run completes so it doesn't linger after the reply. */}
          {showBottomSpacer ? (
            <div className="min-h-[60vh] shrink-0" aria-hidden="true" />
          ) : null}
        </div>

        {historyLoading ? (
          <div className="absolute inset-0 overflow-hidden bg-white dark:bg-zinc-950">
            <div className="flex flex-col gap-8 p-6">
              {/* Skeleton chat bubbles — alternating user (right) / assistant (left) */}
              {([72, 56] as const).map((_, outerIdx) =>
                [false, true, false].map((isUser, innerIdx) => {
                  const key = outerIdx * 3 + innerIdx;
                  const w = [240, 160, 300][innerIdx];
                  return (
                    <div
                      key={key}
                      className={`flex items-start gap-3 ${isUser ? 'flex-row-reverse' : ''
                        }`}
                    >
                      <div className="shimmer-pulse h-9 w-9 shrink-0 rounded-full" />
                      <div
                        className={`flex flex-col gap-2 ${isUser ? 'items-end' : 'items-start'
                          }`}
                      >
                        <div className="shimmer-pulse h-3 w-14 rounded" />
                        <div
                          className="shimmer-pulse h-12 rounded-xl"
                          style={{ width: w, maxWidth: '72vw' }}
                        />
                      </div>
                    </div>
                  );
                }),
              )}
            </div>
          </div>
        ) : null}
      </div>

      {/* Composer */}
      <div
        className="border-t border-zinc-200 bg-white px-4 py-3 dark:border-zinc-800 dark:bg-zinc-950"
        onDragOver={(e) => { e.preventDefault(); e.stopPropagation(); }}
        onDrop={(e) => {
          e.preventDefault();
          e.stopPropagation();
          if (e.dataTransfer.files?.length) appendFiles(e.dataTransfer.files);
        }}
      >
        <div className="mx-auto max-w-4xl">
          <Sender
            key={composerResetKey}
            value={input}
            loading={canCancel}
            onChange={(value) => setInput(value)}
            onSubmit={() => void handleSend()}
            onCancel={() => void handleCancel()}
            onPasteFile={(files) => appendFiles(files)}
            placeholder={t('chat.placeholder')}
            submitType="shiftEnter"
            autoSize={{ minRows: 1, maxRows: 4 }}
            rootClassName="agent-sender-simple"
            suffix={false}
            header={
              attachments.length ? (
                <div className="px-2 pt-2">
                  <FileCard.List
                    items={attachmentItems}
                    size="small"
                    overflow="wrap"
                    removable
                    onRemove={(item) => removeAttachment(String(item.key))}
                  />
                </div>
              ) : null
            }
            footer={(actionNode) => (
              <div className="flex items-center justify-between gap-3">
                <div className="flex items-center">
                  <Dropdown
                    trigger={['click']}
                    menu={{
                      items: [
                        {
                          key: 'image',
                          label: t('chat.imageAttachment'),
                          icon: (
                            <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect width="18" height="18" x="3" y="3" rx="2" ry="2" /><circle cx="9" cy="9" r="2" /><path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" /></svg>
                          ),
                          onClick: () => imageInputRef.current?.click(),
                        },
                        {
                          key: 'file',
                          label: t('chat.fileAttachment'),
                          icon: (
                            <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 8.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48" /></svg>
                          ),
                          onClick: () => fileInputRef.current?.click(),
                        },
                      ],
                    }}
                  >
                    <button
                      type="button"
                      className="flex h-7 w-7 items-center justify-center rounded-md text-zinc-400 transition-colors hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
                      title={t('chat.addAttachment')}
                    >
                      <Plus className="h-4 w-4" />
                    </button>
                  </Dropdown>
                </div>
                <div className="flex items-center gap-2">
                  {actionNode}
                </div>
              </div>
            )}
          />
          <input
            ref={imageInputRef}
            type="file"
            accept="image/*"
            multiple
            className="hidden"
            onChange={(e) => {
              if (e.target.files) appendFiles(e.target.files);
              e.target.value = '';
            }}
          />
          <input
            ref={fileInputRef}
            type="file"
            accept="image/*,.pdf,.txt,.md,.json,.csv,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.zip"
            multiple
            className="hidden"
            onChange={(e) => {
              if (e.target.files) appendFiles(e.target.files);
              e.target.value = '';
            }}
          />
        </div>
      </div>
    </div>
  );
}

import React, { useCallback, useEffect, useRef, useState } from 'react';
import type { BubbleItemType } from '@ant-design/x';
import { ThoughtChain } from '@ant-design/x';
import { XMarkdown } from '@ant-design/x-markdown';
import { Avatar, Button, Tag, Image, Space, Tooltip } from 'antd';
import { BrainCircuit, Check, Copy, Loader2, Shield, ShieldAlert, ShieldCheck, ShieldQuestion, Sparkles, Wrench } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import type { TranslationKey } from '@/lib/i18n/translations';
import { apiURL } from '@/lib/api';
import type { ChatHistoryResponse } from '@/lib/api';
import type { RunTrace, RunTraceMessage, LocalTurn } from './types';
import { readString, formatTime } from './utils';
import { createEmptyTrace } from './trace-utils';
import { AttachmentThumbnails } from './ImagePreview';

// ---------------------------------------------------------------------------
// Render helpers – pure functions that return JSX, no hooks / state
// ---------------------------------------------------------------------------

export function renderPreformatted(text: string) {
    return (
        <pre className="w-full max-w-full overflow-x-auto whitespace-pre-wrap rounded-xl bg-zinc-950 px-3 py-2 text-xs leading-6 text-zinc-100">
            {text}
        </pre>
    );
}

/**
 * Streaming markdown renderer — paragraph-gated fade-in.
 *
 * Why paragraph-gating is the ONLY reliable way to prevent "pop-in":
 *   CSS `animation` fires once when a DOM node is *inserted*. After 1.2 s the
 *   animation completes and the <p> sits at opacity:1 permanently. Any text
 *   appended to that same <p> by XMarkdown reconciliation appears instantly at
 *   opacity:1 — no animation, hence the occasional "pop".
 *
 * Solution — two buckets:
 *   visibleText  = text up to and including the last completed \n\n boundary.
 *                  Rendered inside `.stream-reveal-active`: each newly inserted
 *                  block element (new <p>, <pre>, <ul> …) fades in via CSS
 *                  animation. Already-visible elements keep opacity:1 forever.
 *   livePart     = the current in-progress paragraph (after the last \n\n).
 *                  Rendered at opacity:0 — invisible, but occupies layout space
 *                  so the viewport height doesn't jump when it completes.
 *                  When \n\n arrives it moves to visibleText and a fresh <p>
 *                  node is inserted into `.stream-reveal-active` → animation ✓.
 *
 * End-of-stream: the parent switches from <StreamingMessage> to renderMarkdown
 *   (see renderAssistantTurnContent). The livePart that was hidden appears at
 *   opacity:1 in the final render — a one-time reveal at the very end, which
 *   is visually acceptable and expected ("response is complete").
 */
export function StreamingMessage({
    streamedText,
    isDark,
}: {
    streamedText: string;
    isDark: boolean;
}) {
    // visibleText holds only complete paragraphs (up to the last \n\n).
    const [visibleText, setVisibleText] = useState('');

    const latestRef = useRef(streamedText);
    useEffect(() => {
        latestRef.current = streamedText;
    }, [streamedText]);

    useEffect(() => {
        const id = setInterval(() => {
            const full = latestRef.current;
            const breakIdx = findSafeParagraphBreak(full);
            if (breakIdx < 0) return;
            const committed = full.slice(0, breakIdx + 2);
            // Only advance forward — never shrink.
            setVisibleText((prev) => (committed.length > prev.length ? committed : prev));
        }, 100);
        return () => clearInterval(id);
    }, []);

    // livePart = text after the last \n\n (may be empty).
    // We pass `streamedText` directly so it always reflects the latest SSE data.
    const lastBreak = findSafeParagraphBreak(streamedText);
    const livePart = lastBreak >= 0 ? streamedText.slice(lastBreak + 2) : streamedText;

    // Ratchet: track the maximum rendered height and apply it as minHeight so
    // the bubble can only grow, never shrink, during streaming. This prevents
    // the height flicker that occurs when a completed paragraph moves from the
    // invisible livePart to the fade-in visibleText bucket — the new <p> node
    // starts at opacity:0 / height:0 in the animation's first frame, causing a
    // brief dip. We write directly to DOM style to avoid React re-renders.
    const containerRef = useRef<HTMLDivElement>(null);
    useEffect(() => {
        const el = containerRef.current;
        if (!el) return;
        let maxH = 0;
        const ro = new ResizeObserver(() => {
            const h = el.offsetHeight;
            if (h > maxH) {
                maxH = h;
                el.style.minHeight = `${maxH}px`;
            }
        });
        ro.observe(el);
        return () => {
            ro.disconnect();
            // Reset minHeight once streaming is done and the component unmounts.
            el.style.minHeight = '';
        };
    }, []);

    return (
        <div ref={containerRef}>
            {/* Completed paragraphs — each new block element fades in via CSS animation */}
            {visibleText ? (
                <div className="stream-reveal-active">
                    {renderMarkdown(visibleText, isDark)}
                </div>
            ) : null}
            {/* In-progress paragraph — hidden so no text ever pops in.
                Occupies layout space so height is reserved and won't jump. */}
            {livePart ? (
                <div style={{ opacity: 0 }} aria-hidden="true">
                    {renderMarkdown(livePart, isDark)}
                </div>
            ) : null}
        </div>
    );
}

/**
 * Find the index of the last \n\n that is NOT inside a fenced code block.
 * Returns -1 when no safe boundary exists.
 */
function findSafeParagraphBreak(text: string): number {
    let lastSafe = -1;
    let fenceOpen = false;
    let i = 0;
    while (i < text.length) {
        // Detect opening/closing ``` fence at start of a line.
        if (
            text[i] === '`' && text[i + 1] === '`' && text[i + 2] === '`' &&
            (i === 0 || text[i - 1] === '\n')
        ) {
            fenceOpen = !fenceOpen;
            i += 3;
            continue;
        }
        if (!fenceOpen && text[i] === '\n' && text[i + 1] === '\n') {
            lastSafe = i;
        }
        i += 1;
    }
    return lastSafe;
}

export function renderMarkdown(text: string, isDark: boolean) {
    return (
        <XMarkdown
            content={text}
            rootClassName={isDark ? 'x-markdown-dark agent-markdown' : 'x-markdown-light agent-markdown'}
        />
    );
}

/**
 * Copy button shown below each bubble — icon-only, matching antd Bubble footer demo style.
 */
export function CopyButton({ text }: { text: string }) {
    const { t } = useI18n();
    const [copied, setCopied] = useState(false);
    const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    const handleCopy = useCallback(() => {
        if (!text) return;
        void navigator.clipboard.writeText(text).then(() => {
            setCopied(true);
            if (timerRef.current) clearTimeout(timerRef.current);
            timerRef.current = setTimeout(() => setCopied(false), 2000);
        });
    }, [text]);

    useEffect(() => () => { if (timerRef.current) clearTimeout(timerRef.current); }, []);

    return (
        <Space size={4}>
            <Tooltip title={copied ? t('common.copied') : t('common.copy')}>
                <Button
                    type="text"
                    size="small"
                    icon={copied
                        ? <Check className="h-3.5 w-3.5 text-emerald-500" />
                        : <Copy className="h-3.5 w-3.5" />
                    }
                    onClick={handleCopy}
                />
            </Tooltip>
        </Space>
    );
}

/**
 * Render media URLs as images. Handles both file:// and http(s):// URLs.
 * For file:// URLs, proxies through the API to read local files.
 */
export function renderMediaUrls(
    t: (key: TranslationKey, params?: Record<string, string>) => string,
    mediaUrl?: string,
    mediaUrls?: string[],
) {
    const urls: string[] = [];
    if (mediaUrl) urls.push(mediaUrl);
    if (mediaUrls) urls.push(...mediaUrls);

    if (urls.length === 0) return null;

    return (
        <div className="mt-3 flex flex-wrap gap-2">
            {urls.map((url, index) => {
                // Convert file:// URLs to API proxy URLs
                const displayUrl = url.startsWith('file://')
                    ? apiURL(`/api/workspace/media?path=${encodeURIComponent(url)}`)
                    : url;

                return (
                    <Image
                        key={index}
                        src={displayUrl}
                        alt={t('chat.attachmentAlt', { index: String(index + 1) })}
                        className="max-h-80 rounded-lg object-contain"
                        style={{ maxWidth: 320 }}
                        placeholder
                        fallback="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
                    />
                );
            })}
        </div>
    );
}

export function wrapAssistantBubble(content: React.ReactNode) {
    return (
        <div
            className="min-w-0 w-full max-w-[42rem] transition-[max-width] duration-200 ease-out"
        >
            {content}
        </div>
    );
}

export function renderTraceContent(
    trace: RunTrace,
    t: (key: TranslationKey) => string,
) {
    if (!trace.steps.length) return null;
    return <TraceContentInner trace={trace} t={t} />;
}

/**
 * Module-level cache that maps each runId (thinkingKey) to its expand/collapse
 * state.  This survives React component remounts caused by Bubble.List
 * reconciliation during SSE streaming, so the user's manual toggle is never
 * lost.
 */
const _expandedCache = new Map<string, string[]>();

const TraceContentInner = React.memo(function TraceContentInner({
    trace,
    t,
}: {
    trace: RunTrace;
    t: (key: TranslationKey) => string;
}) {
    const isRunning = trace.status === 'running';
    const thinkingKey = trace.runId || 'thinking';

    // Restore from cache if available; otherwise default to expanded.
    const [expandedKeys, setExpandedKeys] = useState<string[]>(
        () => _expandedCache.get(thinkingKey) ?? [thinkingKey],
    );

    // Persist every change to the module-level cache so that if the
    // component remounts we pick up the same state.
    const handleExpand = useCallback((keys: string[]) => {
        _expandedCache.set(thinkingKey, keys);
        // Defer setState to avoid "Cannot update component while rendering"
        // when ThoughtChain fires onExpand during its own render phase.
        queueMicrotask(() => setExpandedKeys(keys));
    }, [thinkingKey]);

    // Build a single content node that interleaves reasoning text and tool-call status lines
    const contentSegments = trace.steps.map((step) => {
        if (step.kind === 'reasoning') {
            const text = step.content || '';
            if (!text) return null;
            return (
                <div key={step.key} className="whitespace-pre-wrap break-words text-[13px] leading-6 text-zinc-600 dark:text-zinc-300">
                    {text}
                </div>
            );
        }

        // Tool call — render as a compact status line with expandable details
        const toolName = step.title || 'tool';
        const reviewState = step.reviewStatus?.state;

        // Determine display status & color
        let statusLabel: string;
        let statusColor: 'processing' | 'success' | 'error' | 'warning' | 'default';

        if (step.status === 'error') {
            statusLabel = t('chat.traceFailed');
            statusColor = 'error';
        } else if (step.status === 'success') {
            statusLabel = t('chat.traceCompleted');
            statusColor = 'success';
        } else {
            statusLabel = t('chat.traceRunning');
            statusColor = 'processing';
        }

        // Review icon-only (tooltip carries the text)
        let reviewIcon: React.ReactNode = null;
        if (reviewState && reviewState !== 'skipped') {
            const reviewConfig: Record<
                string,
                { icon: React.ReactNode; label: TranslationKey; color: string }
            > = {
                reviewing: {
                    icon: (
                        <span className="relative inline-flex h-4 w-4 items-center justify-center text-blue-500 dark:text-blue-400">
                            <ShieldQuestion className="h-3.5 w-3.5" />
                            <Loader2 className="absolute -right-1 -bottom-1 h-2 w-2 animate-spin" />
                        </span>
                    ),
                    label: 'chat.reviewReviewing',
                    color: 'text-blue-500 dark:text-blue-400',
                },
                approved: {
                    icon: <ShieldCheck className="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400" />,
                    label: 'chat.reviewApproved',
                    color: 'text-emerald-600 dark:text-emerald-400',
                },
                flagged: {
                    icon: <ShieldAlert className="h-3.5 w-3.5 text-amber-500 dark:text-amber-400" />,
                    label: 'chat.reviewFlagged',
                    color: 'text-amber-500 dark:text-amber-400',
                },
                rejected: {
                    icon: <Shield className="h-3.5 w-3.5 text-red-500 dark:text-red-400" />,
                    label: 'chat.reviewRejected',
                    color: 'text-red-500 dark:text-red-400',
                },
            };
            const rc = reviewConfig[reviewState];
            if (rc) {
                // Build rich tooltip content showing tool name, args, and review details
                const tooltipContent = (
                    <div className="max-w-xs space-y-1 text-xs">
                        <div className="font-semibold">{t(rc.label)}</div>
                        <div className="flex gap-1">
                            <span className="shrink-0 text-zinc-400">{t('chat.reviewToolLabel')}:</span>
                            <span className="font-mono">{toolName}</span>
                        </div>
                        {step.toolArgs && Object.keys(step.toolArgs).length > 0 ? (
                            <div>
                                <span className="text-zinc-400">{t('chat.reviewArgsLabel')}:</span>
                                <pre className="mt-0.5 max-h-40 overflow-auto whitespace-pre-wrap rounded bg-black/20 px-1.5 py-1 font-mono text-[11px] leading-4">
                                    {JSON.stringify(step.toolArgs, null, 2)}
                                </pre>
                            </div>
                        ) : null}
                        {step.reviewStatus?.risk ? (
                            <div className="flex gap-1">
                                <span className="shrink-0 text-zinc-400">{t('chat.reviewRiskLabel')}:</span>
                                <span>{step.reviewStatus.risk}</span>
                            </div>
                        ) : null}
                        {step.reviewStatus?.reason ? (
                            <div className="flex gap-1">
                                <span className="shrink-0 text-zinc-400">{t('chat.reviewReasonLabel')}:</span>
                                <span>{step.reviewStatus.reason}</span>
                            </div>
                        ) : null}
                    </div>
                );
                reviewIcon = (
                    <Tooltip title={tooltipContent} placement="top" styles={{ root: { maxWidth: 360 } }}>
                        <span className="inline-flex cursor-help items-center">
                            {rc.icon}
                        </span>
                    </Tooltip>
                );
            }
        }

        const hasContent = Boolean(step.content);

        return (
            <details key={step.key} className="group rounded-md bg-zinc-100/80 dark:bg-zinc-800/60">
                <summary className="flex cursor-pointer list-none items-center gap-1.5 px-2.5 py-1.5 text-[12px] marker:hidden">
                    <Wrench className="h-3 w-3 shrink-0 text-zinc-400 dark:text-zinc-500" />
                    <span className="font-medium text-zinc-700 dark:text-zinc-300">{toolName}</span>
                    <Tag
                        variant="filled"
                        color={statusColor}
                        className="inline-flex h-5 items-center px-1.5 text-[10px]"
                    >
                        {statusLabel}
                    </Tag>
                    {reviewIcon}
                    {hasContent ? (
                        <span className="ml-auto inline-flex h-4 w-4 items-center justify-center text-[10px] text-zinc-400 transition-transform group-open:rotate-90">
                            ▸
                        </span>
                    ) : null}
                </summary>
                {hasContent ? (
                    <div className="border-t border-zinc-200/60 px-2.5 pb-2 pt-1.5 dark:border-zinc-700/50">
                        {renderPreformatted(step.content!)}
                    </div>
                ) : null}
            </details>
        );
    });

    // Overall ThoughtChain status
    const chainStatus: 'loading' | 'success' | 'error' =
        trace.status === 'failed' ? 'error'
            : trace.status === 'completed' ? 'success'
                : 'loading';

    return (
        <ThoughtChain
            expandedKeys={expandedKeys}
            onExpand={handleExpand}
            items={[{
                key: thinkingKey,
                icon: <BrainCircuit className="h-3.5 w-3.5" />,
                title: t('chat.traceTitle'),
                description: undefined,
                status: chainStatus,
                collapsible: true,
                blink: isRunning,
                content: (
                    <div className="flex flex-col gap-2">
                        {contentSegments}
                    </div>
                ),
            }]}
            styles={{
                itemContent: { padding: '8px 0 4px' },
            }}
        />
    );
});

export function renderAssistantLoading(t: (key: TranslationKey) => string) {
    return wrapAssistantBubble(
        <div className="bubble-content-in flex min-h-[2.5rem] items-center justify-center rounded-xl bg-white/90 p-3 dark:bg-zinc-950/80">
            <div className="inline-flex items-center gap-1 text-zinc-400 dark:text-zinc-500">
                <span className="typing-dot h-1.5 w-1.5 rounded-full bg-current" />
                <span className="typing-dot h-1.5 w-1.5 rounded-full bg-current [animation-delay:0.15s]" />
                <span className="typing-dot h-1.5 w-1.5 rounded-full bg-current [animation-delay:0.3s]" />
                <span className="sr-only">{t('chat.traceRunning')}</span>
            </div>
        </div>,
    );
}

export function renderAssistantTurnContent(
    finalText: string,
    trace: RunTrace | null,
    isDark: boolean,
    t: (key: TranslationKey) => string,
    mediaUrl?: string,
    mediaUrls?: string[],
) {
    const streamedText = trace?.streamedText || '';
    const visibleText = finalText.trim() || streamedText.trim();
    const hasTrace = Boolean(trace && trace.steps.length);
    const hasFinal = Boolean(visibleText);
    const hasMedia = Boolean(mediaUrl || (mediaUrls && mediaUrls.length));
    const isStreaming = trace?.status === 'running';
    // Use fade-in streaming renderer when still receiving server tokens and no finalText from history yet.
    const useStreamingRender = isStreaming && !finalText.trim() && Boolean(streamedText);

    if (!hasTrace && !hasFinal && !hasMedia) return '';

    return wrapAssistantBubble(
        <div className="bubble-content-in overflow-hidden rounded-xl bg-white/90 p-2.5 dark:bg-zinc-950/80">
            {hasTrace && trace ? renderTraceContent(trace, t) : null}
            {hasFinal ? (
                <div
                    className={
                        hasTrace ? 'mt-2.5 border-t border-zinc-200 pt-2.5 dark:border-zinc-800' : ''
                    }
                >
                    {useStreamingRender ? (
                        <StreamingMessage streamedText={streamedText} isDark={isDark} />
                    ) : (
                        <>
                            {renderMarkdown(visibleText, isDark)}
                            {isStreaming && visibleText ? (
                                <span className="ml-2 inline-flex align-middle text-zinc-400">
                                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                                </span>
                            ) : null}
                        </>
                    )}
                </div>
            ) : null}
            {hasMedia ? renderMediaUrls(t, mediaUrl, mediaUrls) : null}
        </div>,
    );
}

/**
 * Render a single finalized message (text + media) in a bubble wrapper.
 * Optionally includes trace steps (thinking/tool) above the text.
 */
function renderFinalizedMessageBubble(
    msg: RunTraceMessage,
    trace: RunTrace | null,
    isDark: boolean,
    t: (key: TranslationKey) => string,
): React.ReactNode {
    // Build a static trace (status:completed, no streamedText) so the
    // thinking section does not show a spinner or streaming indicators.
    const stepsTrace: RunTrace | null =
        trace && trace.steps.length > 0
            ? { ...trace, streamedText: '', pendingStreamedText: '', finalizedMessages: [], status: 'completed' }
            : null;
    const hasTrace = Boolean(stepsTrace);
    const hasText = Boolean(msg.text.trim());
    const hasMedia = Boolean(msg.mediaUrl || (msg.mediaUrls && msg.mediaUrls.length));

    if (!hasTrace && !hasText && !hasMedia) return '';

    return wrapAssistantBubble(
        <div className="bubble-content-in overflow-hidden rounded-xl bg-white/90 p-2.5 dark:bg-zinc-950/80">
            {hasTrace && stepsTrace ? renderTraceContent(stepsTrace, t) : null}
            {hasText ? (
                <div className={hasTrace ? 'mt-2.5 border-t border-zinc-200 pt-2.5 dark:border-zinc-800' : ''}>
                    {renderMarkdown(msg.text, isDark)}
                </div>
            ) : null}
            {hasMedia ? renderMediaUrls(t, msg.mediaUrl, msg.mediaUrls) : null}
        </div>,
    );
}

// ---------------------------------------------------------------------------
// History → Bubble mapping
// ---------------------------------------------------------------------------

export function matchLocalTurns(
    messages: NonNullable<ChatHistoryResponse['messages']>,
    turns: LocalTurn[],
) {
    const matches = new Map<string, { userIndex: number; assistantIndices: number[] }>();
    let searchStart = 0;

    turns.forEach((turn) => {
        let userIndex = -1;
        for (let index = searchStart; index < messages.length; index += 1) {
            const message = messages[index];
            if (
                readString(message?.role).toLowerCase() === 'user' &&
                readString(message?.text) === turn.userText
            ) {
                userIndex = index;
                break;
            }
        }
        if (userIndex === -1) return;

        // Collect ALL assistant messages that follow this user message
        // until we hit the next user message or end of array.
        const assistantIndices: number[] = [];
        for (let index = userIndex + 1; index < messages.length; index += 1) {
            const message = messages[index];
            const role = readString(message?.role).toLowerCase();
            const type = readString(message?.type).toLowerCase();
            if (role === 'user') break;
            if (role === 'assistant' && (type === '' || type === 'assistant_final')) {
                assistantIndices.push(index);
            }
        }

        matches.set(turn.id, { userIndex, assistantIndices });
        searchStart = userIndex + 1;
    });

    return matches;
}

export function toBubbleItems(
    history: ChatHistoryResponse | null,
    turns: LocalTurn[],
    traces: Record<string, RunTrace>,
    isDark: boolean,
    t: (key: TranslationKey) => string,
): BubbleItemType[] {
    const items: BubbleItemType[] = [];
    const messages = Array.isArray(history?.messages) ? history?.messages : [];
    const localTurnMatches = matchLocalTurns(messages, turns);
    const skippedIndexes = new Set<number>();

    localTurnMatches.forEach((match) => {
        skippedIndexes.add(match.userIndex);
        match.assistantIndices.forEach((idx) => skippedIndexes.add(idx));
    });

    messages.forEach((message, index) => {
        if (skippedIndexes.has(index)) return;

        const role = readString(message?.role).toLowerCase();
        const type = readString(message?.type).toLowerCase();
        const text = readString(message?.text);
        const timestamp = formatTime(readString(message?.timestamp));
        const key = `${readString(message?.timestamp) || 'msg'}-${index}`;

        // Skip internal orchestrator messages (subagent announcements, tool
        // calls/results, compaction) that should not appear in the chat UI.
        if (type === 'subagent_completion' || type === 'tool_call' || type === 'tool_result' || type === 'compaction') return;

        if (role === 'user' && (text || message?.mediaUrl || message?.mediaUrls?.length)) {
            const userText = text || '';
            items.push({
                key,
                role: 'user',
                content: (
                    <>
                        {text || null}
                        {renderMediaUrls(t, message?.mediaUrl, message?.mediaUrls)}
                    </>
                ),
                header: timestamp ? (
                    <div className="flex items-center gap-2 text-xs text-zinc-500">
                        <span>{timestamp}</span>
                    </div>
                ) : undefined,
                footer: <CopyButton text={userText} />,
            });
            return;
        }

        if (role === 'assistant' && (type === '' || type === 'assistant_final') && (text || message?.mediaUrl || message?.mediaUrls?.length)) {
            items.push({
                key,
                role: 'assistant',
                content: (
                    <>
                        {text ? renderMarkdown(text, isDark) : null}
                        {renderMediaUrls(t, message?.mediaUrl, message?.mediaUrls)}
                    </>
                ),
                header: timestamp ? (
                    <div className="flex items-center gap-2 text-xs text-zinc-500">
                        <span>{timestamp}</span>
                    </div>
                ) : undefined,
                footer: text ? <CopyButton text={text} /> : undefined,
            });
            return;
        }
    });

    turns.forEach((turn) => {
        const match = localTurnMatches.get(turn.id);

        // Collect ALL traces across all RunIDs for this turn
        const allTraces = turn.runIds
            .map((rid) => traces[rid])
            .filter((t): t is RunTrace => Boolean(t));
        const lastTrace = allTraces.length > 0 ? allTraces[allTraces.length - 1] : null;
        const isRunning = lastTrace?.status === 'running';

        // Collect history assistant messages for this turn
        const historyAssistantMsgs = match
            ? match.assistantIndices.map((i) => messages[i]).filter(Boolean)
            : [];
        // Collect ALL finalized messages across ALL traces (strict RunID-based)
        const allFinalizedMsgs = allTraces.flatMap((tr) => tr.finalizedMessages);
        const streamingText = lastTrace?.streamedText?.trim() || '';
        const turnActive = turn.status === 'pending' || turn.status === 'running';

        // Use trace data during active run or when history hasn't loaded yet;
        // once completed AND history has the messages, prefer history (authoritative).
        // Also prefer trace when it has more messages than history — this prevents
        // a brief downward-count flicker during the completion transition when
        // history might be stale (the async refreshHistory hasn't returned yet).
        const useTraceData = turnActive || historyAssistantMsgs.length === 0 || allFinalizedMsgs.length > historyAssistantMsgs.length;

        const turnTimestamp = formatTime(turn.createdAt);

        // Skip the user bubble for orphan / external runs (no user text, no attachments).
        const hasUserContent = Boolean(turn.userText) || Boolean(turn.attachments?.length);
        if (hasUserContent) {
            items.push({
                key: `${turn.id}-user`,
                role: 'user',
                content: (
                    <>
                        {turn.userText}
                        {turn.attachments ? (
                            <AttachmentThumbnails attachments={turn.attachments} />
                        ) : null}
                    </>
                ),
                header: turnTimestamp ? (
                    <div className="flex items-center gap-2 text-xs text-zinc-500">
                        <span>{turnTimestamp}</span>
                    </div>
                ) : undefined,
                footer: turn.userText ? <CopyButton text={turn.userText} /> : undefined,
            });
        }

        if (useTraceData) {
            // ── Real-time rendering from trace ──────────────────────
            let bubbleIndex = 0;

            // Finalized messages across all traces (each gets its own bubble)
            allFinalizedMsgs.forEach((msg, idx) => {
                // Attach trace steps (thinking/tools) to the FIRST message only
                const stepsTrace = idx === 0 && allTraces.length > 0 ? allTraces[0] : null;
                const content = renderFinalizedMessageBubble(msg, stepsTrace, isDark, t);
                items.push({
                    key: `${turn.id}-assistant-${bubbleIndex}`,
                    role: 'assistant',
                    content: content || '',
                    loading: false,
                    typing: false,
                    footer: msg.text ? <CopyButton text={msg.text} /> : undefined,
                });
                bubbleIndex += 1;
            });

            // Currently streaming message (if text is arriving)
            if (isRunning && streamingText) {
                // Build a streaming-only trace: no finalized messages, steps only
                // if this is the first bubble (no finalized messages rendered yet).
                const streamTrace: RunTrace = {
                    ...(lastTrace || createEmptyTrace('')),
                    finalizedMessages: [],
                    steps: allFinalizedMsgs.length === 0 ? (lastTrace?.steps || []) : [],
                };
                items.push({
                    key: `${turn.id}-assistant-${bubbleIndex}`,
                    role: 'assistant',
                    content: renderAssistantTurnContent('', streamTrace, isDark, t),
                    loading: false,
                    typing: false,
                    header: (
                        <div className="flex items-center gap-2 text-xs text-zinc-500">
                            <Tag color="processing">{t('chat.traceRunning')}</Tag>
                        </div>
                    ),
                });
                bubbleIndex += 1;
            }

            // No content yet — show loading or trace-only bubble
            if (bubbleIndex === 0) {
                if (isRunning && lastTrace && lastTrace.steps.length > 0) {
                    // Has thinking/tool steps but no text yet
                    items.push({
                        key: `${turn.id}-assistant`,
                        role: 'assistant',
                        content: renderAssistantTurnContent('', lastTrace, isDark, t),
                        loading: false,
                        typing: false,
                        header: (
                            <div className="flex items-center gap-2 text-xs text-zinc-500">
                                <Tag color="processing">{t('chat.traceRunning')}</Tag>
                            </div>
                        ),
                    });
                } else if (turn.status === 'pending' || turn.status === 'running') {
                    items.push({
                        key: `${turn.id}-assistant`,
                        role: 'assistant',
                        content: renderAssistantLoading(t),
                        loading: false,
                        typing: false,
                    });
                } else {
                    // Turn completed/failed/canceled with no messages — empty bubble
                    items.push({
                        key: `${turn.id}-assistant`,
                        role: 'assistant',
                        content: '',
                        loading: false,
                        typing: false,
                    });
                }
            }
        } else {
            // ── Completed turn — render from history messages ───────
            historyAssistantMsgs.forEach((msg, idx) => {
                const text = readString(msg?.text);
                const timestamp = formatTime(readString(msg?.timestamp));
                // Attach trace steps to the first assistant bubble
                const stepsTrace = idx === 0 && allTraces.length > 0 ? allTraces[0] : null;
                const content = renderFinalizedMessageBubble(
                    { text, mediaUrl: msg?.mediaUrl, mediaUrls: msg?.mediaUrls },
                    stepsTrace,
                    isDark,
                    t,
                );
                items.push({
                    key: `${turn.id}-assistant-${idx}`,
                    role: 'assistant',
                    content: content || '',
                    loading: false,
                    typing: false,
                    header: timestamp ? (
                        <div className="flex items-center gap-2 text-xs text-zinc-500">
                            <span>{timestamp}</span>
                        </div>
                    ) : undefined,
                    footer: text ? <CopyButton text={text} /> : undefined,
                });
            });
        }
    });

    return items;
}

// ---------------------------------------------------------------------------
// Bubble.List role config (stable reference)
// ---------------------------------------------------------------------------

export const BUBBLE_ROLES = {
    user: {
        placement: 'end' as const,
        variant: 'filled' as const,
    },
    assistant: {
        placement: 'start' as const,
        variant: 'outlined' as const,
        avatar: (
            <img src="/logo.svg" alt="logo" className="h-7 w-7" />
        ),
    },
    tool_call: {
        placement: 'start' as const,
        variant: 'outlined' as const,
        avatar: (
            <Avatar
                size={36}
                className="bg-amber-500 text-white"
                icon={<Wrench className="h-4 w-4" />}
            />
        ),
    },
    tool_result: {
        placement: 'start' as const,
        variant: 'outlined' as const,
        avatar: (
            <Avatar
                size={36}
                className="bg-emerald-500 text-white"
                icon={<Sparkles className="h-4 w-4" />}
            />
        ),
    },
};

// ---------------------------------------------------------------------------
// Chat domain types — used across all chat sub-modules
// ---------------------------------------------------------------------------

export type CerebellumReviewStatus = {
    state: 'skipped' | 'reviewing' | 'approved' | 'flagged' | 'rejected';
    reason?: string;
    risk?: string;
};

export type RunTraceStep = {
    key: string;
    kind?: 'reasoning' | 'tool';
    title: string;
    description?: string;
    summary?: string;
    content?: string;
    pendingContent?: string;
    status?: 'loading' | 'success' | 'error';
    /** Cerebellum safety review status for this tool call. */
    reviewStatus?: CerebellumReviewStatus;
    /** Tool call arguments/parameters (captured from SSE events). */
    toolArgs?: Record<string, unknown>;
};

/** A single finalized assistant message within a run (one per `assistant/final` SSE event). */
export type RunTraceMessage = {
    text: string;
    mediaUrl?: string;
    mediaUrls?: string[];
};

export type RunTrace = {
    runId: string;
    /** Text being actively streamed for the CURRENT (latest) message. Reset on each `final`. */
    streamedText: string;
    pendingStreamedText: string;
    /**
     * Set to true when an `assistant/final` event is received.
     * Prevents replayed `text_delta` events (re-emitted by the server after the run
     * completes) from appending to streamedText a second time and causing duplication.
     * Reset to false when a `tool` event arrives (signalling a new round of output).
     */
    streamingLocked?: boolean;
    /**
     * Set to true when an `assistant/yield` event is received.
     * While yielded, the lifecycle/run_completed event will NOT transition
     * status to 'completed', keeping the turn in 'running' state so the
     * rendering path stays stable throughout the yield-resume cycle.
     */
    yielded?: boolean;
    /** Completed assistant messages — one entry per `assistant/final` SSE event. */
    finalizedMessages: RunTraceMessage[];
    steps: RunTraceStep[];
    nextStepOrdinal: number;
    status: 'running' | 'completed' | 'failed';
    updatedAt?: string;
};

export type ComposerAttachment = {
    id: string;
    file: File;
    name: string;
    mimeType: string;
    size: number;
    kind: 'image' | 'file';
    previewUrl?: string;
};

export type ChatAttachmentPayload = {
    type: 'image' | 'file';
    mimeType: string;
    fileName: string;
    content: string;
};

export type LocalTurnAttachment = {
    id: string;
    name: string;
    kind: 'image' | 'file';
    previewUrl?: string;
};

export type LocalTurn = {
    id: string;
    userText: string;
    createdAt: string;
    /** Ordered list of all RunIDs for this turn (yield-resume creates multiple). */
    runIds: string[];
    status: 'pending' | 'running' | 'completed' | 'failed' | 'canceled';
    attachments?: LocalTurnAttachment[];
};

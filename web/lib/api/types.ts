// ---------------------------------------------------------------------------
// API response & domain types
// ---------------------------------------------------------------------------

export type ChatHistoryMessage = {
    role: string;
    text: string;
    type?: string;
    timestamp?: string;
    runId?: string;
    toolCallId?: string;
    toolName?: string;
    args?: Record<string, unknown>;
    mediaUrl?: string;
    mediaUrls?: string[];
};

export type SkillSnapshotSummary = {
    version?: number;
    skillNames?: string[];
    commandNames?: string[];
};

export type ChatHistoryResponse = {
    sessionKey: string;
    sessionId?: string;
    skillsSnapshot?: SkillSnapshotSummary | null;
    messages?: ChatHistoryMessage[] | null;
    total?: number;
    hasMore?: boolean;
    nextBefore?: number;
};

export type ChatSendResponse = {
    runId: string;
    sessionKey: string;
    sessionId?: string;
    skillsSnapshot?: SkillSnapshotSummary | null;
    payloads: Array<{ text?: string }>;
    messages?: ChatHistoryMessage[] | null;
    queued?: boolean;
    queueDepth?: number;
};

export type ChatCancelResponse = {
    sessionKey: string;
    skillsSnapshot?: SkillSnapshotSummary | null;
    aborted?: boolean;
    runIDs?: string[];
    clearedQueued?: number;
    payloads?: Array<{ text?: string }>;
    messages?: ChatHistoryMessage[] | null;
};

export type TaskRecord = {
    id: string;
    agentId?: string;
    sessionKey?: string;
    title?: string;
    message: string;
    status: string;
    payloadKind?: string;
    sessionTarget?: string;
    wakeMode?: string;
    deliveryMode?: string;
    deliveryBestEffort?: boolean;
    failureAlertAfter?: number;
    failureAlertCooldownMs?: number;
    failureAlertChannel?: string;
    failureAlertTo?: string;
    failureAlertAccountId?: string;
    failureAlertMode?: string;
    scheduleKind?: string;
    scheduleAt?: string;
    scheduleEveryMs?: number;
    scheduleAnchorMs?: number;
    scheduleExpr?: string;
    scheduleTz?: string;
    scheduleStaggerMs?: number;
    nextRunAt?: string;
    intervalSeconds?: number;
    createdAt?: string;
    updatedAt?: string;
    channel?: string;
    to?: string;
    accountId?: string;
    threadId?: string;
    deliver?: boolean;
    lastError?: string;
};

export type BrainState = {
    defaultAgent: string;
    agents: {
        defaults?: Record<string, unknown>;
        list?: Array<Record<string, unknown>>;
    };
    models: {
        providers?: Record<string, {
            baseUrl?: string;
            api?: string;
            apiKey?: string;
            models?: Array<{
                id: string;
                name?: string;
                reasoning?: boolean;
                input?: string[];
                contextWindow?: number;
                maxTokens?: number;
            }>;
            command?: Record<string, unknown>;
        }>;
    };
    providers?: Array<{
        provider: string;
        backendKind?: string;
        configured: boolean;
        ready: boolean;
        modelCount?: number;
        lastError?: string;
    }>;
    systemPrompt?: string;
    modelRecords?: BrainModelRecord[];
    modelPresets?: BrainModelPreset[];
    brainMode?: string;
    brainLocal?: LocalModelState;
    cerebellum?: CerebellumState;
};

export type BrainModelRecord = {
    key: string;
    providerId: string;
    modelId: string;
    displayName?: string;
    baseUrl?: string;
    api?: string;
    apiKey?: string;
    reasoning?: boolean;
    contextWindow?: number;
    maxTokens?: number;
    isDefault?: boolean;
    isFallback?: boolean;
    ready?: boolean;
    lastError?: string;
};

export type BrainModelPreset = {
    id: string;
    label: string;
    labelZh?: string;
    free?: boolean;
    baseUrl: string;
    api: string;
    models: Array<{
        id: string;
        name?: string;
        reasoning?: boolean;
        contextWindow?: number;
        maxTokens?: number;
    }>;
    authKind?: string;  // 'oauth-device-code' | undefined (default: apikey)
    oauthConfig?: {
        deviceCodeUrl: string;
        tokenUrl: string;
        clientId: string;
        scope: string;
    };
};

export type OAuthDeviceCodeStartResponse = {
    sessionId: string;
    userCode: string;
    verificationUrl: string;
    expiresIn: number;
    interval: number;
};

export type OAuthDeviceCodePollResponse = {
    status: 'pending' | 'success' | 'error' | 'expired';
    accessToken?: string;
    baseUrl?: string;
    error?: string;
};

export type OAuthStatusResponse = {
    authenticated: Record<string, boolean>;
};

export type LocalizedText = string | {
    zh?: string;
    en?: string;
};

export type LocalModelState = {
    enabled: boolean;
    status: 'running' | 'stopped' | 'starting' | 'stopping' | 'error';
    modelId?: string;
    modelsDir?: string;
    models: Array<{
        id: string;
        name: string;
        size?: string;
    }>;
    catalog?: Array<{
        id: string;
        name: string;
        description?: LocalizedText;
        size?: string;
        downloadUrl?: string;
        filename?: string;
        defaults?: ModelPresetDefaults;
    }>;
    lastError?: string;
    downloadProgress?: {
        presetId: string;
        filename: string;
        totalBytes: number;
        downloadedBytes: number;
        active: boolean;
        canceled?: boolean;
        error?: string;
    };
    autoStart?: boolean;
    enableThinking?: boolean;
    sampling?: SamplingParams;
    threads?: number;
    contextSize?: number;
    gpuLayers?: number;
};

export type CerebellumState = {
    enabled: boolean;
    status: 'running' | 'stopped' | 'starting' | 'stopping' | 'error';
    modelId?: string;
    modelsDir?: string;
    models: Array<{
        id: string;
        name: string;
        size?: string;
    }>;
    catalog?: Array<{
        id: string;
        name: string;
        description?: LocalizedText;
        size?: string;
        downloadUrl?: string;
        filename?: string;
        defaults?: ModelPresetDefaults;
    }>;
    lastError?: string;
    downloadProgress?: {
        presetId: string;
        filename: string;
        totalBytes: number;
        downloadedBytes: number;
        active: boolean;
        canceled?: boolean;
        error?: string;
    };
    autoStart?: boolean;
    enableThinking?: boolean;
    sampling?: SamplingParams;
    threads?: number;
    contextSize?: number;
    gpuLayers?: number;
};

export type ModelPresetDefaults = {
    threads?: number;
    contextSize?: number;
    gpuLayers?: number;
    enableThinking?: boolean;
    sampling?: SamplingParams;
};

export type SamplingParams = {
    temp: number;
    topP: number;
    topK: number;
    minP: number;
    typicalP: number;
    repeatLastN: number;
    penaltyRepeat: number;
    penaltyFreq: number;
    penaltyPresent: number;
};

export type CapabilitiesState = {
    skills: {
        workspaceDir?: string;
        version?: number;
        skills?: Array<{
            name: string;
            description?: string;
            skillKey?: string;
            filePath?: string;
            baseDir?: string;
            source?: string;
            disabled?: boolean;
            eligible?: boolean;
            blockedByAllowlist?: boolean;
            missingEnv?: string[];
            missingConfig?: string[];
        }>;
    };
    tools: Array<{
        name: string;
        description?: string;
        allowed: boolean;
        optional?: boolean;
        elevated?: boolean;
        ownerOnly?: boolean;
        pluginId?: string;
    }>;
    plugins: Array<{
        id: string;
        enabled: boolean;
    }>;
    skillsConfig: {
        entries?: Record<string, {
            enabled?: boolean;
        }>;
    };
};

export type DataState = {
    defaultAgent: string;
    workspace: string;
    systemPrompt?: string;
    files: Array<{
        name: string;
        path: string;
        exists: boolean;
        content: string;
    }>;
};

export type SandboxState = {
    defaultAgent: string;
    agents: Array<{
        agentId: string;
        workspaceDir?: string;
        agentDir?: string;
        sandboxEnabled?: boolean;
        sandboxDirs?: string[];
    }>;
};

export type ChannelFieldType = 'text' | 'password' | 'select' | 'checkbox' | 'number';

export type ChannelConfigField = {
    key: string;
    label: string;
    type: ChannelFieldType;
    required: boolean;
    placeholder?: string;
    defaultValue?: string;
    options?: Array<{ value: string; label: string }>;
    help?: string;
    group?: string;
};

export type ChannelDriverSchema = {
    id: string;
    name: string;
    description?: string;
    fields: ChannelConfigField[];
};

export type ChannelsState = {
    config: {
        defaults?: Record<string, unknown>;
        entries?: Record<string, {
            enabled?: boolean;
            agent?: string;
            defaultTo?: string;
            defaultAccount?: string;
            allowFrom?: string[];
            chunkMode?: string;
            textChunkLimit?: number;
            accounts?: Record<string, Record<string, unknown>>;
            config?: Record<string, unknown>;
        }>;
    };
    integrations: Array<{
        id: string;
        enabled: boolean;
        hasTransport: boolean;
        hasOutbound: boolean;
        supportsHTTPIngress: boolean;
        supportsStreamingReplies: boolean;
        deliveryMode?: string;
    }>;
    schemas: ChannelDriverSchema[];
};

export type DashboardSnapshot = {
    occurredAt: string;
    runtime: {
        healthy: boolean;
        configuredAgent?: string;
        activeSubagents?: number;
    };
    activeRuns: {
        total: number;
        bySession?: Record<string, number>;
    };
    deliveryQueue: {
        pending: number;
        failed: number;
    };
    tasks: {
        total: number;
        running: number;
        queued: number;
        scheduled: number;
        failed: number;
    };
    providers?: Array<{
        provider: string;
        backendKind?: string;
        configured: boolean;
        ready: boolean;
        modelCount?: number;
        lastError?: string;
    }>;
    brainMode?: string;
    brainLocalStatus?: string;
    cerebellumStatus?: string;
};

export type AuditEvent = {
    occurredAt?: string;
    category: string;
    type: string;
    level?: string;
    agentId?: string;
    message?: string;
    sessionKey?: string;
    runId?: string;
    taskId?: string;
    toolName?: string;
    channel?: string;
    data?: Record<string, unknown>;
};

export type AuditListRequest = {
    category?: string;
    type?: string;
    level?: string;
    text?: string;
    sessionKey?: string;
    runId?: string;
    taskId?: string;
    limit?: number;
};

export type EnvironmentState = {
    environment: {
        strict?: boolean;
        entries?: Record<string, {
            value?: string;
            fromEnv?: string;
            masked?: boolean;
            required?: boolean;
        }>;
    };
    resolved?: Record<string, string>;
    masked?: Record<string, string>;
};

export type SkillFile = {
    name: string;
    size: number;
};

export type SkillFilesResponse = {
    files: SkillFile[];
};

export type SkillFileContentResponse = {
    name: string;
    content: string;
};

export type SkillImportValidateResponse = {
    valid: boolean;
    error?: string;
    name?: string;
    description?: string;
    skillDir?: string;
    tempDir?: string;
    source?: string;
};

// ─────────────────────────────────────────────────────────────────
// Network / Proxy
// ─────────────────────────────────────────────────────────────────

export type NetworkState = {
    useSystemProxy: boolean;
    proxyUrl: string;
    language: 'system' | 'en' | 'zh';
};

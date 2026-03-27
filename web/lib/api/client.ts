'use client';

type APIErrorPayload = {
    code?: unknown;
    error?: unknown;
    message?: unknown;
};

function isStructuredErrorCode(value: unknown): value is string {
    return typeof value === 'string' && /^[A-Z0-9_]+$/.test(value.trim());
}

export class APIClientError extends Error {
    code: string;
    status: number;

    constructor(message: string, options?: { code?: string; status?: number }) {
        super(message);
        this.name = 'APIClientError';
        this.code = options?.code || '';
        this.status = options?.status || 0;
    }
}

function normalizeBaseURL(value: string): string {
    return value.replace(/\/+$/, '');
}

export function getAPIBaseURL(): string {
    return 'http://127.0.0.1:18789';
    const configured = (process.env.NEXT_PUBLIC_kocort_API_BASE_URL || '').trim();
    if (configured) {
        return normalizeBaseURL(configured);
    }
    if (typeof window !== 'undefined' && window.location?.origin) {
        return normalizeBaseURL(window.location.origin);
    }
    return 'http://127.0.0.1:18789/';
}

export function apiURL(path: string): string {
    const normalizedPath = path.startsWith('/') ? path : `/${path}`;
    return `${getAPIBaseURL()}${normalizedPath}`;
}

async function parseResponse<T>(response: Response): Promise<T> {
    if (!response.ok) {
        const text = await response.text();
        let payload: APIErrorPayload | null = null;
        try {
            payload = text ? JSON.parse(text) as APIErrorPayload : null;
        } catch {
            payload = null;
        }
        const code = isStructuredErrorCode(payload?.code)
            ? payload.code.trim()
            : isStructuredErrorCode(payload?.error)
                ? payload.error.trim()
                : '';
        const message = typeof payload?.message === 'string' && payload.message.trim()
            ? payload.message.trim()
            : typeof payload?.error === 'string' && payload.error.trim()
                ? payload.error.trim()
                : text || `${response.status} ${response.statusText}`;
        throw new APIClientError(message, { code, status: response.status });
    }
    return response.json() as Promise<T>;
}

export async function apiGet<T>(path: string): Promise<T> {
    const response = await fetch(apiURL(path), {
        method: 'GET',
        headers: { Accept: 'application/json' },
        cache: 'no-store',
    });
    return parseResponse<T>(response);
}

export async function apiPost<T>(path: string, body: unknown): Promise<T> {
    const response = await fetch(apiURL(path), {
        method: 'POST',
        headers: {
            Accept: 'application/json',
            'Content-Type': 'application/json',
        },
        body: JSON.stringify(body),
    });
    return parseResponse<T>(response);
}

export async function apiPostForm<T>(path: string, formData: FormData): Promise<T> {
    const response = await fetch(apiURL(path), {
        method: 'POST',
        headers: { Accept: 'application/json' },
        body: formData,
    });
    return parseResponse<T>(response);
}

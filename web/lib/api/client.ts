'use client';

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
        throw new Error(text || `${response.status} ${response.statusText}`);
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

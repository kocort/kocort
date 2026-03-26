import type { ChatAttachmentPayload, ComposerAttachment } from './types';

// ---------------------------------------------------------------------------
// General-purpose helper functions for the chat module
// ---------------------------------------------------------------------------

export function asRecord(value: unknown): Record<string, unknown> {
    return value && typeof value === 'object' ? (value as Record<string, unknown>) : {};
}

export function readString(value: unknown): string {
    return typeof value === 'string' ? value.trim() : '';
}

export function readText(value: unknown): string {
    return typeof value === 'string' ? value : '';
}

export function formatTime(value?: string): string {
    if (!value) return '';
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return '';
    return new Intl.DateTimeFormat('zh-CN', {
        hour: '2-digit',
        minute: '2-digit',
    }).format(parsed);
}

export function buildAttachmentId(file: File): string {
    return `${file.name}-${file.size}-${file.lastModified}`;
}

export function isImageFile(file: File): boolean {
    return file.type.startsWith('image/');
}

export function createComposerAttachment(file: File): ComposerAttachment {
    return {
        id: buildAttachmentId(file),
        file,
        name: file.name,
        mimeType: file.type || 'application/octet-stream',
        size: file.size,
        kind: isImageFile(file) ? 'image' : 'file',
        previewUrl: isImageFile(file) ? URL.createObjectURL(file) : undefined,
    };
}

export function readFileAsDataURL(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => {
            if (typeof reader.result === 'string') {
                resolve(reader.result);
                return;
            }
            reject(new Error(`Failed to read ${file.name}`));
        };
        reader.onerror = () => reject(reader.error || new Error(`Failed to read ${file.name}`));
        reader.readAsDataURL(file);
    });
}

export async function toChatAttachmentPayload(attachment: ComposerAttachment): Promise<ChatAttachmentPayload> {
    return {
        type: attachment.kind,
        mimeType: attachment.mimeType,
        fileName: attachment.name,
        content: await readFileAsDataURL(attachment.file),
    };
}

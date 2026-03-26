import { useCallback, useEffect, useRef, useState } from 'react';
import { useI18n } from '@/lib/i18n/I18nContext';
import type { ComposerAttachment } from '../types';
import { createComposerAttachment } from '../utils';

/**
 * Manages file / image attachments for the chat composer.
 * Handles preview-URL lifecycle (create & revoke) automatically.
 */
export function useAttachments() {
    const { t } = useI18n();
    const [attachments, setAttachments] = useState<ComposerAttachment[]>([]);
    const fileInputRef = useRef<HTMLInputElement>(null);
    const imageInputRef = useRef<HTMLInputElement>(null);
    const attachmentsRef = useRef<ComposerAttachment[]>([]);

    const resetInputElements = useCallback(() => {
        if (fileInputRef.current) fileInputRef.current.value = '';
        if (imageInputRef.current) imageInputRef.current.value = '';
    }, []);

    // Keep ref in sync for cleanup
    useEffect(() => {
        attachmentsRef.current = attachments;
    }, [attachments]);

    // Revoke preview URLs on unmount
    useEffect(
        () => () => {
            attachmentsRef.current.forEach((a) => {
                if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
            });
        },
        [],
    );

    const appendFiles = useCallback((fileList: FileList | File[]) => {
        const files = Array.from(fileList);
        if (!files.length) return;
        setAttachments((current) => {
            const next = [...current];
            files.forEach((file) => {
                const item = createComposerAttachment(file);
                if (!next.some((existing) => existing.id === item.id)) {
                    next.push(item);
                } else if (item.previewUrl) {
                    URL.revokeObjectURL(item.previewUrl);
                }
            });
            return next;
        });
    }, []);

    const removeAttachment = useCallback((id: string) => {
        setAttachments((current) => {
            const target = current.find((item) => item.id === id);
            if (target?.previewUrl) URL.revokeObjectURL(target.previewUrl);
            return current.filter((item) => item.id !== id);
        });
    }, []);

    /** Clear composer attachments without revoking preview URLs still used elsewhere. */
    const clearAttachments = useCallback(() => {
        setAttachments([]);
        resetInputElements();
    }, [resetInputElements]);

    /** Revoke all preview URLs and clear the list. */
    const clearAndRevokeAll = useCallback(() => {
        setAttachments((current) => {
            current.forEach((a) => {
                if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
            });
            return [];
        });
        resetInputElements();
    }, [resetInputElements]);

    const attachmentItems = attachments.map((a) => ({
        key: a.id,
        name: a.name,
        description: a.kind === 'image' ? t('chat.imageAttachment') : t('chat.fileAttachment'),
        size: 'small' as const,
        type: a.kind,
        src: a.previewUrl,
        icon: a.kind === 'image' ? 'image' : 'default',
    }));

    return {
        attachments,
        setAttachments,
        appendFiles,
        removeAttachment,
        clearAttachments,
        clearAndRevokeAll,
        attachmentItems,
        fileInputRef,
        imageInputRef,
    };
}

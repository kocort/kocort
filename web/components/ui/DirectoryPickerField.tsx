'use client';

import { useState } from 'react';
import { FolderOpen, Loader2 } from 'lucide-react';
import { apiPost } from '@/lib/api';
import { useI18n } from '@/lib/i18n/I18nContext';
import { cn } from '@/lib/utils';

type BrowseDirResponse = { path: string; cancelled: boolean };

interface DirectoryPickerFieldProps {
    value: string;
    onChange: (value: string) => void;
    placeholder: string;
    browseLabel: string;
    browsePrompt?: string;
    disabled?: boolean;
    variant?: 'card' | 'inline';
    className?: string;
    onBrowseError?: (message: string) => void;
}

export function DirectoryPickerField({
    value,
    onChange,
    placeholder,
    browseLabel,
    browsePrompt,
    disabled = false,
    variant = 'card',
    className,
    onBrowseError,
}: DirectoryPickerFieldProps) {
    const { t } = useI18n();
    const [browsing, setBrowsing] = useState(false);

    const handleBrowse = async () => {
        setBrowsing(true);
        try {
            const res = await apiPost<BrowseDirResponse>('/api/engine/browse-dir', {
                prompt: browsePrompt || browseLabel,
            });
            if (!res.cancelled && res.path) {
                onChange(res.path);
            }
        } catch (err) {
            onBrowseError?.(err instanceof Error ? err.message : t('common.browseDirError'));
        } finally {
            setBrowsing(false);
        }
    };

    if (variant === 'inline') {
        return (
            <div className={cn('flex items-center gap-2', className)}>
                <input
                    type="text"
                    value={value}
                    onChange={(e) => onChange(e.target.value)}
                    placeholder={placeholder}
                    disabled={disabled || browsing}
                    className="flex-1 rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2 text-sm text-zinc-900 outline-none dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
                />
                <button
                    type="button"
                    onClick={() => void handleBrowse()}
                    disabled={disabled || browsing}
                    className="inline-flex items-center gap-2 rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 disabled:cursor-not-allowed disabled:opacity-50 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
                >
                    {browsing ? <Loader2 className="h-4 w-4 animate-spin" /> : <FolderOpen className="h-4 w-4" />}
                    {browseLabel}
                </button>
            </div>
        );
    }

    return (
        <button
            type="button"
            onClick={() => void handleBrowse()}
            disabled={disabled || browsing}
            className={cn(
                'w-full flex items-center justify-center gap-2 rounded-xl border-2 border-dashed border-zinc-300 dark:border-zinc-700 py-6 text-sm font-medium text-zinc-600 dark:text-zinc-300 hover:border-indigo-400 hover:text-indigo-600 dark:hover:border-indigo-500 dark:hover:text-indigo-400 transition-colors disabled:opacity-50',
                className,
            )}
        >
            {browsing ? <Loader2 className="h-5 w-5 animate-spin" /> : <FolderOpen className="h-5 w-5" />}
            {value || placeholder}
        </button>
    );
}
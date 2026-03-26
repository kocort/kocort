import { cn } from '@/lib/utils';

interface ErrorAlertProps {
    message: string;
    className?: string;
}

/**
 * Standardised error banner used across all view components.
 * Renders nothing when `message` is falsy.
 */
export function ErrorAlert({ message, className }: ErrorAlertProps) {
    if (!message) return null;
    return (
        <div
            className={cn(
                'rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700 dark:border-rose-900/50 dark:bg-rose-950/30 dark:text-rose-300',
                className,
            )}
        >
            {message}
        </div>
    );
}

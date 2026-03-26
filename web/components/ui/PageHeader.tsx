import type { ReactNode } from 'react';

interface PageHeaderProps {
    /** Page title */
    title: string;
    /** Short description shown below the title */
    description?: string;
    /** Optional extra metadata rendered below description */
    meta?: ReactNode;
    /** Action buttons / controls rendered on the right side */
    children?: ReactNode;
}

/**
 * Consistent page header with title, description, and optional action area.
 */
export function PageHeader({ title, description, meta, children }: PageHeaderProps) {
    return (
        <div className="border-b border-zinc-200 px-6 py-4 pr-20 dark:border-zinc-800">
            <div className="flex items-start justify-between gap-4">
                <div>
                    <h2 className="text-xl font-semibold tracking-tight text-zinc-900 dark:text-zinc-100">
                        {title}
                    </h2>
                    {description ? (
                        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{description}</p>
                    ) : null}
                    {meta}
                </div>
                {children ? <div className="flex shrink-0 items-center gap-2">{children}</div> : null}
            </div>
        </div>
    );
}

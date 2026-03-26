'use client';

import { AlertTriangle } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { cn } from '@/lib/utils';

interface ConfirmDialogProps {
  isOpen: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  message: string;
  confirmText?: string;
  cancelText?: string;
  variant?: 'danger' | 'warning';
  loading?: boolean;
}

/**
 * Confirmation dialog for destructive actions.
 */
export function ConfirmDialog({
  isOpen,
  onClose,
  onConfirm,
  title,
  message,
  confirmText,
  cancelText,
  variant = 'danger',
  loading = false,
}: ConfirmDialogProps) {
  const { t } = useI18n();

  const handleConfirm = () => {
    onConfirm();
  };

  return (
    <AnimatePresence>
      {isOpen && (
        <>
          {/* Backdrop */}
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={onClose}
            className="fixed inset-0 bg-black/50 backdrop-blur-sm z-50"
          />

          {/* Dialog */}
          <motion.div
            initial={{ opacity: 0, scale: 0.95, y: 20 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.95, y: 20 }}
            transition={{ duration: 0.15 }}
            className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 w-full max-w-sm bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-2xl shadow-xl z-50 overflow-hidden"
          >
            <div className="p-6">
              {/* Icon */}
              <div className={cn(
                'mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full',
                variant === 'danger'
                  ? 'bg-rose-100 dark:bg-rose-500/10'
                  : 'bg-amber-100 dark:bg-amber-500/10'
              )}>
                <AlertTriangle className={cn(
                  'h-6 w-6',
                  variant === 'danger'
                    ? 'text-rose-600 dark:text-rose-400'
                    : 'text-amber-600 dark:text-amber-400'
                )} />
              </div>

              {/* Content */}
              <div className="text-center">
                <h3 className="text-lg font-semibold text-zinc-900 dark:text-zinc-100">
                  {title}
                </h3>
                <p className="mt-2 text-sm text-zinc-500 dark:text-zinc-400">
                  {message}
                </p>
              </div>

              {/* Actions */}
              <div className="mt-6 flex gap-3">
                <button
                  onClick={onClose}
                  disabled={loading}
                  className="flex-1 rounded-lg border border-zinc-200 dark:border-zinc-700 px-4 py-2.5 text-sm font-medium text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors disabled:opacity-50"
                >
                  {cancelText || t('common.cancel')}
                </button>
                <button
                  onClick={handleConfirm}
                  disabled={loading}
                  className={cn(
                    'flex-1 rounded-lg px-4 py-2.5 text-sm font-medium text-white transition-colors disabled:opacity-50',
                    variant === 'danger'
                      ? 'bg-rose-600 hover:bg-rose-500'
                      : 'bg-amber-600 hover:bg-amber-500'
                  )}
                >
                  {loading ? t('common.saving') : confirmText || t('common.confirm')}
                </button>
              </div>
            </div>
          </motion.div>
        </>
      )}
    </AnimatePresence>
  );
}
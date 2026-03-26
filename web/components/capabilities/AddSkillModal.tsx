'use client';

import { useRef, useState } from 'react';
import { CheckCircle, Loader2, Package, Upload, X, XCircle } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { apiPost, apiPostForm, type CapabilitiesState, type SkillImportValidateResponse } from '@/lib/api';
import { DirectoryPickerField } from '@/components/ui';

interface AddSkillModalProps {
    isOpen: boolean;
    onClose: () => void;
    onImported: (state: CapabilitiesState) => void;
}

export function AddSkillModal({ isOpen, onClose, onImported }: AddSkillModalProps) {
    const { t } = useI18n();
    const fileInputRef = useRef<HTMLInputElement>(null);
    const [mode, setMode] = useState<'select' | 'validated'>('select');
    const [dirPath, setDirPath] = useState('');
    const [zipFileName, setZipFileName] = useState('');
    const [zipFile, setZipFile] = useState<File | null>(null);
    const [validating, setValidating] = useState(false);
    const [importing, setImporting] = useState(false);
    const [error, setError] = useState('');
    const [validation, setValidation] = useState<SkillImportValidateResponse | null>(null);

    const reset = () => {
        setMode('select');
        setDirPath('');
        setZipFileName('');
        setZipFile(null);
        setValidating(false);
        setImporting(false);
        setError('');
        setValidation(null);
    };

    const handleClose = () => {
        reset();
        onClose();
    };

    const handleZipSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (!file) return;
        setZipFile(file);
        setZipFileName(file.name);
        setDirPath('');
        setError('');
        setValidation(null);
        setMode('select');
    };

    const handleValidate = async () => {
        setError('');
        setValidating(true);
        try {
            const formData = new FormData();
            if (zipFile) {
                formData.append('zip', zipFile);
            } else if (dirPath.trim()) {
                formData.append('dir', dirPath.trim());
            } else {
                setError(t('cap.selectSourceError'));
                setValidating(false);
                return;
            }
            const res = await apiPostForm<SkillImportValidateResponse>(
                '/api/engine/capabilities/skill/import/validate',
                formData,
            );
            setValidation(res);
            if (res.valid) {
                setMode('validated');
            } else {
                setError(res.error || t('cap.invalidSkill'));
            }
        } catch (err) {
            setError(err instanceof Error ? err.message : t('cap.importError'));
        } finally {
            setValidating(false);
        }
    };

    const handleConfirm = async () => {
        if (!validation?.valid) return;
        setImporting(true);
        setError('');
        try {
            const result = await apiPost<CapabilitiesState>(
                '/api/engine/capabilities/skill/import/confirm',
                {
                    skillDir: validation.skillDir,
                    tempDir: validation.tempDir || '',
                    source: validation.source || 'dir',
                },
            );
            onImported(result);
            handleClose();
        } catch (err) {
            setError(err instanceof Error ? err.message : t('cap.importError'));
        } finally {
            setImporting(false);
        }
    };

    return (
        <AnimatePresence>
            {isOpen && (
                <>
                    <motion.div
                        initial={{ opacity: 0 }}
                        animate={{ opacity: 1 }}
                        exit={{ opacity: 0 }}
                        onClick={handleClose}
                        className="fixed inset-0 bg-black/50 backdrop-blur-sm z-50"
                    />
                    <motion.div
                        initial={{ opacity: 0, scale: 0.95, y: 20 }}
                        animate={{ opacity: 1, scale: 1, y: 0 }}
                        exit={{ opacity: 0, scale: 0.95, y: 20 }}
                        className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 w-full max-w-lg bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-2xl shadow-xl z-50 overflow-hidden"
                    >
                        {/* Header */}
                        <div className="flex items-center justify-between px-6 py-4 border-b border-zinc-200 dark:border-zinc-800">
                            <h3 className="text-lg font-semibold text-zinc-900 dark:text-zinc-100">{t('cap.addSkillTitle')}</h3>
                            <button
                                onClick={handleClose}
                                className="p-2 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200 transition-colors rounded-lg hover:bg-zinc-100 dark:hover:bg-zinc-800"
                            >
                                <X className="w-5 h-5" />
                            </button>
                        </div>

                        {/* Body */}
                        <div className="p-6 space-y-5" style={{ maxHeight: 'calc(85vh - 73px)', overflowY: 'auto' }}>
                            <p className="text-sm text-zinc-500 dark:text-zinc-400">{t('cap.addSkillDesc')}</p>

                            {/* Zip upload */}
                            <div>
                                <input
                                    ref={fileInputRef}
                                    type="file"
                                    accept=".zip"
                                    onChange={handleZipSelect}
                                    className="hidden"
                                />
                                <button
                                    type="button"
                                    onClick={() => fileInputRef.current?.click()}
                                    className="w-full flex items-center justify-center gap-2 rounded-xl border-2 border-dashed border-zinc-300 dark:border-zinc-700 py-6 text-sm font-medium text-zinc-600 dark:text-zinc-300 hover:border-indigo-400 hover:text-indigo-600 dark:hover:border-indigo-500 dark:hover:text-indigo-400 transition-colors"
                                >
                                    <Upload className="h-5 w-5" />
                                    {zipFileName || t('cap.selectZip')}
                                </button>
                                {zipFileName ? (
                                    <div className="mt-2 flex items-center justify-between">
                                        <span className="text-xs text-zinc-500 dark:text-zinc-400 truncate">{zipFileName}</span>
                                        <button
                                            onClick={() => { setZipFile(null); setZipFileName(''); setValidation(null); setMode('select'); }}
                                            className="text-xs text-indigo-600 dark:text-indigo-400 hover:underline"
                                        >
                                            {t('cap.changeFile')}
                                        </button>
                                    </div>
                                ) : null}
                            </div>

                            {/* Divider */}
                            <div className="flex items-center gap-3">
                                <div className="flex-1 border-t border-zinc-200 dark:border-zinc-800" />
                                <span className="text-xs font-medium text-zinc-400 dark:text-zinc-500 uppercase">{t('cap.orDirPath')}</span>
                                <div className="flex-1 border-t border-zinc-200 dark:border-zinc-800" />
                            </div>

                            {/* Directory picker */}
                            <div>
                                <DirectoryPickerField
                                    value={dirPath}
                                    onChange={(nextPath) => {
                                        setDirPath(nextPath);
                                        setZipFile(null);
                                        setZipFileName('');
                                        setValidation(null);
                                        setMode('select');
                                        setError('');
                                    }}
                                    placeholder={t('cap.selectDir')}
                                    browseLabel={t('cap.selectDir')}
                                    browsePrompt={t('cap.selectDir')}
                                    onBrowseError={(message) => setError(message || t('cap.browseDirError'))}
                                />
                                {dirPath ? (
                                    <div className="mt-2 flex items-center justify-between">
                                        <span className="text-xs text-zinc-500 dark:text-zinc-400 truncate" title={dirPath}>{dirPath}</span>
                                        <button
                                            onClick={() => { setDirPath(''); setValidation(null); setMode('select'); }}
                                            className="text-xs text-indigo-600 dark:text-indigo-400 hover:underline shrink-0 ml-2"
                                        >
                                            {t('cap.changeFile')}
                                        </button>
                                    </div>
                                ) : null}
                            </div>

                            {/* Error */}
                            {error ? (
                                <div className="flex items-start gap-2 rounded-lg bg-rose-50 dark:bg-rose-500/10 border border-rose-200 dark:border-rose-500/20 p-3">
                                    <XCircle className="h-4 w-4 text-rose-500 shrink-0 mt-0.5" />
                                    <p className="text-sm text-rose-700 dark:text-rose-300">{error}</p>
                                </div>
                            ) : null}

                            {/* Validation result */}
                            {mode === 'validated' && validation?.valid ? (
                                <div className="rounded-xl border border-emerald-200 dark:border-emerald-500/20 bg-emerald-50 dark:bg-emerald-500/10 p-4 space-y-2">
                                    <div className="flex items-center gap-2">
                                        <CheckCircle className="h-5 w-5 text-emerald-600 dark:text-emerald-400" />
                                        <span className="text-sm font-medium text-emerald-700 dark:text-emerald-300">{t('cap.skillDetected')}</span>
                                    </div>
                                    <div className="pl-7 space-y-1">
                                        <div className="flex items-center gap-2">
                                            <Package className="h-4 w-4 text-emerald-600 dark:text-emerald-400" />
                                            <span className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">{validation.name || t('cap.unnamedSkill')}</span>
                                        </div>
                                        {validation.description ? (
                                            <p className="text-xs text-zinc-600 dark:text-zinc-400 leading-relaxed">{validation.description}</p>
                                        ) : null}
                                    </div>
                                </div>
                            ) : null}

                            {/* Actions */}
                            <div className="flex justify-end gap-2 pt-2">
                                <button
                                    onClick={handleClose}
                                    className="rounded-lg px-4 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 dark:text-zinc-300 dark:hover:bg-zinc-800"
                                >
                                    {t('common.cancel')}
                                </button>
                                {mode === 'select' ? (
                                    <button
                                        onClick={() => void handleValidate()}
                                        disabled={(!zipFile && !dirPath.trim()) || validating}
                                        className="flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-50"
                                    >
                                        {validating ? (
                                            <>
                                                <Loader2 className="h-4 w-4 animate-spin" />
                                                {t('cap.validating')}
                                            </>
                                        ) : (
                                            t('cap.validate')
                                        )}
                                    </button>
                                ) : (
                                    <button
                                        onClick={() => void handleConfirm()}
                                        disabled={importing}
                                        className="flex items-center gap-2 rounded-lg bg-emerald-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-emerald-500 disabled:cursor-not-allowed disabled:opacity-50"
                                    >
                                        {importing ? (
                                            <>
                                                <Loader2 className="h-4 w-4 animate-spin" />
                                                {t('cap.importing')}
                                            </>
                                        ) : (
                                            t('cap.importConfirm')
                                        )}
                                    </button>
                                )}
                            </div>
                        </div>
                    </motion.div>
                </>
            )}
        </AnimatePresence>
    );
}

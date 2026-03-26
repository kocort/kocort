'use client';

import { useCallback, useEffect, useState } from 'react';
import { File, FileText, Loader2, X } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { XMarkdown, type ComponentProps } from '@ant-design/x-markdown';
import { CodeHighlighter } from '@ant-design/x';
import { useI18n } from '@/lib/i18n/I18nContext';
import { useThemeSync } from '@/components/chat/hooks/useThemeSync';
import { apiGet, type SkillFile, type SkillFilesResponse, type SkillFileContentResponse } from '@/lib/api';

interface SkillFilesModalProps {
    isOpen: boolean;
    onClose: () => void;
    skillName: string;
    baseDir: string;
}

const TEXT_EXTENSIONS = new Set([
    '.md', '.txt', '.json', '.yaml', '.yml', '.toml', '.xml',
    '.js', '.ts', '.jsx', '.tsx', '.css', '.scss', '.html',
    '.py', '.go', '.rs', '.sh', '.bash', '.zsh', '.fish',
    '.rb', '.java', '.c', '.cpp', '.h', '.hpp', '.cs',
    '.lua', '.vim', '.conf', '.cfg', '.ini', '.env',
    '.gitignore', '.dockerfile', '.makefile',
]);

function isTextFile(name: string): boolean {
    const lower = name.toLowerCase();
    // Files without extension but known names
    if (['makefile', 'dockerfile', 'readme', 'license', 'changelog'].some(n => lower.endsWith(n))) return true;
    const dot = lower.lastIndexOf('.');
    if (dot === -1) return true; // assume text if no extension
    return TEXT_EXTENSIONS.has(lower.slice(dot));
}

function isMarkdown(name: string): boolean {
    return name.toLowerCase().endsWith('.md');
}

function getFileIcon(name: string) {
    if (isMarkdown(name)) return <FileText className="h-4 w-4 text-indigo-500 shrink-0" />;
    return <File className="h-4 w-4 text-zinc-400 shrink-0" />;
}

/** Strip YAML frontmatter (--- delimited block at the top) from markdown content. */
function stripFrontmatter(raw: string): { meta: Record<string, string>; body: string } {
    const lines = raw.replace(/\r\n/g, '\n').split('\n');
    if (lines.length === 0 || lines[0].trim() !== '---') return { meta: {}, body: raw };
    const meta: Record<string, string> = {};
    let i = 1;
    for (; i < lines.length; i++) {
        const line = lines[i].trim();
        if (line === '---') { i++; break; }
        const colon = line.indexOf(':');
        if (colon > 0) {
            const key = line.slice(0, colon).trim();
            const value = line.slice(colon + 1).trim();
            if (key && value) meta[key] = value;
        }
    }
    return { meta, body: lines.slice(i).join('\n') };
}

const Code: React.FC<ComponentProps> = ({ className, children }) => {
    const lang = className?.match(/language-(\w+)/)?.[1] || '';
    if (typeof children !== 'string') return null;
    return <CodeHighlighter lang={lang}>{children}</CodeHighlighter>;
};

export function SkillFilesModal({ isOpen, onClose, skillName, baseDir }: SkillFilesModalProps) {
    const { t } = useI18n();
    const isDark = useThemeSync();
    const [files, setFiles] = useState<SkillFile[]>([]);
    const [selectedFile, setSelectedFile] = useState<string | null>(null);
    const [content, setContent] = useState<string>('');
    const [loadingFiles, setLoadingFiles] = useState(false);
    const [loadingContent, setLoadingContent] = useState(false);
    const [error, setError] = useState('');

    const loadFiles = useCallback(async () => {
        setLoadingFiles(true);
        setError('');
        try {
            const res = await apiGet<SkillFilesResponse>(
                `/api/engine/capabilities/skill/files?baseDir=${encodeURIComponent(baseDir)}`,
            );
            const sorted = (res.files || []).sort((a, b) => {
                // .md files first, then alphabetical
                const aMd = isMarkdown(a.name) ? 0 : 1;
                const bMd = isMarkdown(b.name) ? 0 : 1;
                if (aMd !== bMd) return aMd - bMd;
                return a.name.localeCompare(b.name);
            });
            setFiles(sorted);
            // Auto-select the first .md file, or first file
            const firstMd = sorted.find((f) => isMarkdown(f.name));
            const autoSelect = firstMd?.name || sorted[0]?.name || null;
            setSelectedFile(autoSelect);
        } catch (err) {
            setError(err instanceof Error ? err.message : t('cap.loadFilesError'));
        } finally {
            setLoadingFiles(false);
        }
    }, [baseDir, t]);

    const loadContent = useCallback(async (file: string) => {
        if (!isTextFile(file)) {
            setContent('');
            return;
        }
        setLoadingContent(true);
        try {
            const res = await apiGet<SkillFileContentResponse>(
                `/api/engine/capabilities/skill/file?baseDir=${encodeURIComponent(baseDir)}&file=${encodeURIComponent(file)}`,
            );
            setContent(res.content || '');
        } catch {
            setContent(`// ${t('cap.loadFileContentError')}`);
        } finally {
            setLoadingContent(false);
        }
    }, [baseDir, t]);

    useEffect(() => {
        if (isOpen && baseDir) {
            void loadFiles();
        }
        return () => {
            setFiles([]);
            setSelectedFile(null);
            setContent('');
            setError('');
        };
    }, [isOpen, baseDir, loadFiles]);

    useEffect(() => {
        if (selectedFile) {
            void loadContent(selectedFile);
        }
    }, [selectedFile, loadContent]);

    useEffect(() => {
        if (isOpen) {
            document.body.style.overflow = 'hidden';
        } else {
            document.body.style.overflow = 'unset';
        }
        return () => { document.body.style.overflow = 'unset'; };
    }, [isOpen]);

    return (
        <AnimatePresence>
            {isOpen && (
                <>
                    <motion.div
                        initial={{ opacity: 0 }}
                        animate={{ opacity: 1 }}
                        exit={{ opacity: 0 }}
                        onClick={onClose}
                        className="fixed inset-0 bg-black/50 backdrop-blur-sm z-50"
                    />
                    <motion.div
                        initial={{ opacity: 0, scale: 0.95, y: 20 }}
                        animate={{ opacity: 1, scale: 1, y: 0 }}
                        exit={{ opacity: 0, scale: 0.95, y: 20 }}
                        className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 w-[95vw] max-w-4xl h-[80vh] bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-2xl shadow-xl z-50 flex flex-col overflow-hidden"
                    >
                        {/* Header */}
                        <div className="flex items-center justify-between px-6 py-4 border-b border-zinc-200 dark:border-zinc-800 shrink-0">
                            <div className="min-w-0">
                                <h3 className="text-lg font-semibold text-zinc-900 dark:text-zinc-100 truncate">
                                    {skillName} — {t('cap.skillFiles')}
                                </h3>
                            </div>
                            <button
                                onClick={onClose}
                                className="ml-4 p-2 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200 transition-colors rounded-lg hover:bg-zinc-100 dark:hover:bg-zinc-800 shrink-0"
                            >
                                <X className="w-5 h-5" />
                            </button>
                        </div>

                        {/* Body */}
                        {loadingFiles ? (
                            <div className="flex flex-1 items-center justify-center text-zinc-500 dark:text-zinc-400">
                                <Loader2 className="mr-2 h-5 w-5 animate-spin" />
                                {t('cap.loadingFiles')}
                            </div>
                        ) : error ? (
                            <div className="flex flex-1 items-center justify-center text-rose-500 dark:text-rose-400 px-6 text-sm">
                                {error}
                            </div>
                        ) : files.length === 0 ? (
                            <div className="flex flex-1 items-center justify-center text-zinc-500 dark:text-zinc-400 text-sm">
                                {t('cap.noFiles')}
                            </div>
                        ) : (
                            <div className="flex flex-1 overflow-hidden">
                                {/* File list sidebar */}
                                <div className="w-56 shrink-0 border-r border-zinc-200 dark:border-zinc-800 overflow-y-auto bg-zinc-50 dark:bg-zinc-950/50">
                                    <nav className="py-2">
                                        {files.map((file) => (
                                            <button
                                                key={file.name}
                                                onClick={() => setSelectedFile(file.name)}
                                                className={`w-full text-left flex items-center gap-2 px-4 py-2 text-sm transition-colors ${selectedFile === file.name
                                                    ? 'bg-indigo-50 dark:bg-indigo-500/10 text-indigo-700 dark:text-indigo-300 font-medium'
                                                    : 'text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800'
                                                    }`}
                                                title={file.name}
                                            >
                                                {getFileIcon(file.name)}
                                                <span className="truncate">{file.name}</span>
                                            </button>
                                        ))}
                                    </nav>
                                </div>

                                {/* Content area */}
                                <div className="flex-1 overflow-auto">
                                    {!selectedFile ? (
                                        <div className="flex h-full items-center justify-center text-zinc-400 dark:text-zinc-500 text-sm">
                                            {t('cap.selectFile')}
                                        </div>
                                    ) : loadingContent ? (
                                        <div className="flex h-full items-center justify-center text-zinc-500 dark:text-zinc-400">
                                            <Loader2 className="mr-2 h-5 w-5 animate-spin" />
                                        </div>
                                    ) : !isTextFile(selectedFile) ? (
                                        <div className="flex h-full items-center justify-center text-zinc-400 dark:text-zinc-500 text-sm">
                                            {t('cap.binaryFile')}
                                        </div>
                                    ) : isMarkdown(selectedFile) ? (
                                        (() => {
                                            const { meta, body } = stripFrontmatter(content);
                                            const metaEntries = Object.entries(meta);
                                            return (
                                                <div className="p-5">
                                                    {metaEntries.length > 0 && (
                                                        <div className="mb-4 flex flex-wrap gap-2">
                                                            {metaEntries.map(([k, v]) => (
                                                                <span key={k} className="inline-flex items-center gap-1 rounded-md bg-indigo-50 dark:bg-indigo-500/10 px-2 py-1 text-xs font-medium text-indigo-700 dark:text-indigo-300 ring-1 ring-inset ring-indigo-200 dark:ring-indigo-500/20">
                                                                    <span className="text-indigo-500 dark:text-indigo-400">{k}:</span> {v}
                                                                </span>
                                                            ))}
                                                        </div>
                                                    )}
                                                    <XMarkdown
                                                        content={body}
                                                        className={isDark ? 'x-markdown-dark' : 'x-markdown-light'}
                                                        components={{ code: Code }}
                                                    />
                                                </div>
                                            );
                                        })()
                                    ) : (
                                        <pre className="p-5 text-sm leading-relaxed text-zinc-800 dark:text-zinc-200 whitespace-pre-wrap break-words font-mono">
                                            {content}
                                        </pre>
                                    )}
                                </div>
                            </div>
                        )}
                    </motion.div>
                </>
            )}
        </AnimatePresence>
    );
}

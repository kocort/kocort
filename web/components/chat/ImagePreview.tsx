'use client';

import { useState, useEffect } from 'react';
import { X } from 'lucide-react';

type ImagePreviewModalProps = {
    src: string;
    alt: string;
    onClose: () => void;
};

export function ImagePreviewModal({ src, alt, onClose }: ImagePreviewModalProps) {
    // Close on escape key
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') onClose();
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [onClose]);

    return (
        <div
            className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4"
            onClick={onClose}
        >
            <button
                onClick={onClose}
                className="absolute right-4 top-4 rounded-full bg-white/10 p-2 text-white/80 transition-colors hover:bg-white/20 hover:text-white"
            >
                <X className="h-6 w-6" />
            </button>
            <img
                src={src}
                alt={alt}
                className="max-h-[90vh] max-w-[90vw] rounded-lg object-contain shadow-2xl"
                onClick={(e) => e.stopPropagation()}
            />
        </div>
    );
}

type ImageThumbnailProps = {
    src: string;
    alt: string;
};

export function ImageThumbnail({ src, alt }: ImageThumbnailProps) {
    const [previewOpen, setPreviewOpen] = useState(false);

    return (
        <>
            <button
                type="button"
                className="group relative overflow-hidden rounded-lg border border-zinc-200 bg-zinc-100 transition-all hover:border-zinc-300 hover:shadow-md dark:border-zinc-700 dark:bg-zinc-800 dark:hover:border-zinc-600"
                onClick={() => setPreviewOpen(true)}
            >
                <img
                    src={src}
                    alt={alt}
                    className="h-20 w-20 object-cover transition-transform group-hover:scale-105"
                />
                <div className="absolute inset-0 flex items-center justify-center bg-black/0 transition-colors group-hover:bg-black/10">
                    <svg
                        className="h-6 w-6 text-white opacity-0 transition-opacity group-hover:opacity-100"
                        xmlns="http://www.w3.org/2000/svg"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="2"
                        strokeLinecap="round"
                        strokeLinejoin="round"
                    >
                        <path d="M15 3h6v6M9 21H3v-6M21 3l-7 7M3 21l7-7" />
                    </svg>
                </div>
            </button>
            {previewOpen ? (
                <ImagePreviewModal
                    src={src}
                    alt={alt}
                    onClose={() => setPreviewOpen(false)}
                />
            ) : null}
        </>
    );
}

type AttachmentThumbnailsProps = {
    attachments: Array<{
        id: string;
        name: string;
        kind: 'image' | 'file';
        previewUrl?: string;
    }>;
};

export function AttachmentThumbnails({ attachments }: AttachmentThumbnailsProps) {
    if (!attachments.length) return null;

    const images = attachments.filter((a) => a.kind === 'image' && a.previewUrl);

    if (!images.length) return null;

    return (
        <div className="mt-2 flex flex-wrap gap-2">
            {images.map((image) => (
                <ImageThumbnail
                    key={image.id}
                    src={image.previewUrl!}
                    alt={image.name}
                />
            ))}
        </div>
    );
}
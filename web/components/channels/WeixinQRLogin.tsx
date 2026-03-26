'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { QrCode, CheckCircle2, RefreshCw, Loader2, Smartphone } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { apiPost } from '@/lib/api';

type QRState = 'idle' | 'loading' | 'showing' | 'scanned' | 'confirmed' | 'expired' | 'error';

interface QRStartResponse {
    qrcode: string;
    qrcodeImgContent: string;
}

interface QRPollResponse {
    status: string; // wait, scaned, confirmed, expired
    botToken?: string;
    botId?: string;
    baseUrl?: string;
}

interface WeixinQRLoginProps {
    baseUrl?: string;
    autoStart?: boolean;
    onLoginSuccess: (token: string, baseUrl?: string) => void;
}

export function WeixinQRLogin({ baseUrl, autoStart, onLoginSuccess }: WeixinQRLoginProps) {
    const { t } = useI18n();
    const [qrState, setQRState] = useState<QRState>('idle');
    const [qrImgData, setQRImgData] = useState('');
    const [qrCode, setQRCode] = useState('');
    const [errorMsg, setErrorMsg] = useState('');
    const pollingRef = useRef(false);
    const abortRef = useRef<AbortController | null>(null);

    const stopPolling = useCallback(() => {
        pollingRef.current = false;
        abortRef.current?.abort();
        abortRef.current = null;
    }, []);

    const startQR = useCallback(async () => {
        stopPolling();
        setQRState('loading');
        setErrorMsg('');
        setQRImgData('');
        setQRCode('');

        try {
            const res = await apiPost<QRStartResponse>('/api/integrations/channels/weixin/qr/start', {
                baseUrl: baseUrl || '',
            });
            if (!res.qrcodeImgContent && !res.qrcode) {
                throw new Error('Empty QR code response');
            }
            setQRCode(res.qrcode);
            // Backend returns data URI (base64) or raw base64 string
            const imgContent = res.qrcodeImgContent;
            if (imgContent) {
                setQRImgData(
                    imgContent.startsWith('data:')
                        ? imgContent
                        : `data:image/png;base64,${imgContent}`
                );
            }
            setQRState('showing');
        } catch (err) {
            setErrorMsg(err instanceof Error ? err.message : String(err));
            setQRState('error');
        }
    }, [baseUrl, stopPolling]);

    // Poll loop — runs when qrState is 'showing' or 'scanned'
    useEffect(() => {
        if (qrState !== 'showing' && qrState !== 'scanned') return;
        if (!qrCode) return;

        pollingRef.current = true;

        const poll = async () => {
            while (pollingRef.current) {
                try {
                    const controller = new AbortController();
                    abortRef.current = controller;

                    const res = await apiPost<QRPollResponse>('/api/integrations/channels/weixin/qr/poll', {
                        baseUrl: baseUrl || '',
                        qrcode: qrCode,
                    });

                    if (!pollingRef.current) break;

                    switch (res.status) {
                        case 'scaned':
                            setQRState('scanned');
                            break;
                        case 'confirmed':
                            pollingRef.current = false;
                            setQRState('confirmed');
                            if (res.botToken) {
                                onLoginSuccess(res.botToken, res.baseUrl);
                            }
                            return;
                        case 'expired':
                            pollingRef.current = false;
                            setQRState('expired');
                            return;
                        case 'wait':
                        default:
                            // continue polling
                            break;
                    }
                } catch {
                    // Network error — wait a moment then retry
                    if (!pollingRef.current) break;
                    await new Promise((r) => setTimeout(r, 2000));
                }
            }
        };

        void poll();

        return () => {
            stopPolling();
        };
    }, [qrState, qrCode, baseUrl, onLoginSuccess, stopPolling]);

    // Auto-start QR if requested
    useEffect(() => {
        if (autoStart && qrState === 'idle') {
            void startQR();
        }
    }, [autoStart]); // eslint-disable-line react-hooks/exhaustive-deps

    // Cleanup on unmount
    useEffect(() => stopPolling, [stopPolling]);

    return (
        <div className="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900/50 p-4">
            <div className="flex items-center gap-2 mb-3">
                <QrCode className="w-4 h-4 text-indigo-500" />
                <span className="text-sm font-medium text-zinc-700 dark:text-zinc-300">
                    {t('channels.weixin.qrLogin' as any)}
                </span>
            </div>

            {/* Idle - show start button */}
            {qrState === 'idle' && (
                <button
                    onClick={() => void startQR()}
                    className="flex items-center gap-2 w-full justify-center py-3 px-4 bg-green-600 hover:bg-green-500 text-white rounded-lg text-sm font-medium transition-colors"
                >
                    <QrCode className="w-4 h-4" />
                    {t('channels.weixin.scanToLogin' as any)}
                </button>
            )}

            {/* Loading */}
            {qrState === 'loading' && (
                <div className="flex flex-col items-center py-8 gap-3">
                    <Loader2 className="w-8 h-8 text-indigo-500 animate-spin" />
                    <span className="text-sm text-zinc-500">{t('channels.weixin.loadingQR' as any)}</span>
                </div>
            )}

            {/* Showing QR code */}
            {(qrState === 'showing' || qrState === 'scanned') && (
                <div className="flex flex-col items-center gap-3">
                    <div className="relative bg-white rounded-xl p-3 shadow-sm">
                        <img
                            src={qrImgData}
                            alt={t('channels.weixin.qrAlt' as any)}
                            className="w-48 h-48 object-contain"
                        />
                        {qrState === 'scanned' && (
                            <div className="absolute inset-0 bg-white/80 dark:bg-zinc-900/80 rounded-xl flex flex-col items-center justify-center gap-2">
                                <Smartphone className="w-8 h-8 text-green-500" />
                                <span className="text-sm font-medium text-green-600">
                                    {t('channels.weixin.scanned' as any)}
                                </span>
                            </div>
                        )}
                    </div>
                    <p className="text-xs text-zinc-500 dark:text-zinc-400 text-center">
                        {qrState === 'scanned'
                            ? t('channels.weixin.confirmOnPhone' as any)
                            : t('channels.weixin.scanHint' as any)}
                    </p>
                </div>
            )}

            {/* Confirmed */}
            {qrState === 'confirmed' && (
                <div className="flex flex-col items-center py-6 gap-3">
                    <CheckCircle2 className="w-10 h-10 text-green-500" />
                    <span className="text-sm font-medium text-green-600 dark:text-green-400">
                        {t('channels.weixin.loginSuccess' as any)}
                    </span>
                </div>
            )}

            {/* Expired */}
            {qrState === 'expired' && (
                <div className="flex flex-col items-center py-6 gap-3">
                    <span className="text-sm text-zinc-500">{t('channels.weixin.qrExpired' as any)}</span>
                    <button
                        onClick={() => void startQR()}
                        className="flex items-center gap-2 px-4 py-2 bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg text-sm font-medium transition-colors"
                    >
                        <RefreshCw className="w-4 h-4" />
                        {t('channels.weixin.refresh' as any)}
                    </button>
                </div>
            )}

            {/* Error */}
            {qrState === 'error' && (
                <div className="flex flex-col items-center py-6 gap-3">
                    <span className="text-sm text-rose-500">{errorMsg || t('channels.weixin.qrError' as any)}</span>
                    <button
                        onClick={() => void startQR()}
                        className="flex items-center gap-2 px-4 py-2 bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg text-sm font-medium transition-colors"
                    >
                        <RefreshCw className="w-4 h-4" />
                        {t('channels.weixin.retry' as any)}
                    </button>
                </div>
            )}
        </div>
    );
}

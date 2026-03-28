import { useEffect, useRef } from 'react';

/**
 * Manages scroll-anchoring for the chat messages viewport.
 *
 * Design (Gemini-like):
 * - NO automatic scroll-to-bottom during streaming; content just grows downward.
 * - `scrollToLastUserBubble()` scrolls so the last user message appears near the
 *   top of the viewport — called on initial load and on every send.
 * - `scrollToLastUserBubbleWhenStable()` additionally re-scrolls via ResizeObserver
 *   until the inner content container stops changing height (handles XMarkdown /
 *   syntax-highlight / image reflows that happen after initial DOM commit).
 * - `scrollToBottomInstant()` is a fallback for edge cases.
 */
export function useScrollAnchor(_bubbleItems: unknown[]) {
    const messagesViewportRef = useRef<HTMLDivElement>(null);
    const historyTopSentinelRef = useRef<HTMLDivElement>(null);
    /**
     * Attach to the inner content container (the `max-w-4xl` flex column).
     * ResizeObserver watches this element so reflows caused by XMarkdown /
     * code-highlight / images are detected and the scroll is corrected.
     */
    const contentContainerRef = useRef<HTMLDivElement>(null);
    const isNearBottomRef = useRef(true);
    const stableScrollCleanupRef = useRef<(() => void) | null>(null);

    // Track whether user is scrolled near the bottom (used for load-more guard).
    useEffect(() => {
        const viewport = messagesViewportRef.current;
        if (!viewport) return;
        const onScroll = () => {
            isNearBottomRef.current =
                viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight < 80;
        };
        viewport.addEventListener('scroll', onScroll, { passive: true });
        return () => viewport.removeEventListener('scroll', onScroll);
    }, []);

    /** Instant jump to the very bottom. */
    const scrollToBottomInstant = () => {
        const viewport = messagesViewportRef.current;
        if (viewport) viewport.scrollTop = viewport.scrollHeight;
    };

    const clearStableScroll = () => {
        if (stableScrollCleanupRef.current) {
            stableScrollCleanupRef.current();
            stableScrollCleanupRef.current = null;
        }
    };

    /**
     * Core scroll: positions the last user bubble ~72 px below the viewport top.
     * Uses getBoundingClientRect rather than offsetTop so intermediate positioned
     * ancestors (e.g. Bubble.List wrappers) don't skew the calculation.
     */
    const scrollToLastUserBubble = () => {
        const viewport = messagesViewportRef.current;
        if (!viewport) return;
        const userBubbles =
            viewport.querySelectorAll<HTMLElement>('.ant-bubble-end');
        const last = userBubbles[userBubbles.length - 1];
        if (last) {
            const vpTop = viewport.getBoundingClientRect().top;
            const elTop = last.getBoundingClientRect().top;
            // current scroll + visual offset from viewport edge - desired margin
            viewport.scrollTop = Math.max(0, viewport.scrollTop + elTop - vpTop - 72);
        } else {
            viewport.scrollTop = viewport.scrollHeight;
        }
        isNearBottomRef.current = false;
    };

    /**
     * Re-applies the user-bubble anchor for a short stabilization window.
     * This overrides browser scroll restoration on refresh and late layout
     * shifts from hydrated content inside assistant bubbles.
     */
    const scrollToLastUserBubbleWhenStable = () => {
        const viewport = messagesViewportRef.current;
        const content = contentContainerRef.current;
        if (!viewport) return;

        clearStableScroll();
        scrollToLastUserBubble();

        let frameId = 0;
        let timeoutId = 0;
        let observer: ResizeObserver | null = null;
        let remainingFrames = 6;

        const resync = () => {
            scrollToLastUserBubble();
            if (remainingFrames <= 0) return;
            remainingFrames -= 1;
            frameId = window.requestAnimationFrame(resync);
        };

        frameId = window.requestAnimationFrame(resync);

        if (content && typeof ResizeObserver !== 'undefined') {
            observer = new ResizeObserver(() => {
                scrollToLastUserBubble();
            });
            observer.observe(content);
        }

        timeoutId = window.setTimeout(() => {
            clearStableScroll();
        }, 500);

        stableScrollCleanupRef.current = () => {
            if (frameId) window.cancelAnimationFrame(frameId);
            if (timeoutId) window.clearTimeout(timeoutId);
            observer?.disconnect();
            observer = null;
        };
    };

    useEffect(() => clearStableScroll, []);

    return {
        messagesViewportRef,
        historyTopSentinelRef,
        contentContainerRef,
        isNearBottomRef,
        scrollToBottomInstant,
        scrollToLastUserBubble,
        scrollToLastUserBubbleWhenStable,
    };
}

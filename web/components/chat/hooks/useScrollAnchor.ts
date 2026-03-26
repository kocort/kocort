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

    return {
        messagesViewportRef,
        historyTopSentinelRef,
        contentContainerRef,
        isNearBottomRef,
        scrollToBottomInstant,
        scrollToLastUserBubble,
    };
}

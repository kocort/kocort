'use client';

import { useState, useRef, useEffect, useMemo, useCallback, ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { ChevronDown, Check, Search } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { cn } from '@/lib/utils';
import { useI18n } from '@/lib/i18n/I18nContext';

export interface SelectOption {
  value: string;
  label: string;
  /** Optional rich content rendered in dropdown instead of plain label */
  labelNode?: ReactNode;
  disabled?: boolean;
}

interface SelectProps {
  value: string;
  onChange: (value: string) => void;
  options: SelectOption[];
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  /** Enable search/filter inside the dropdown */
  searchable?: boolean;
  searchPlaceholder?: string;
  /** Allow the user to type a custom value that does not exist in options */
  allowCustomValue?: boolean;
}

/**
 * Custom select dropdown with smooth animations matching the project's design language.
 * Uses a Portal so the dropdown is never clipped by parent overflow / modal containers.
 */
export function Select({
  value,
  onChange,
  options,
  placeholder,
  disabled,
  className = '',
  searchable = false,
  searchPlaceholder,
  allowCustomValue = false,
}: SelectProps) {
  const { t } = useI18n();
  const [isOpen, setIsOpen] = useState(false);
  const [search, setSearch] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const [dropdownPos, setDropdownPos] = useState<{ top: number; left: number; width: number } | null>(null);
  const placeholderText = placeholder || t('common.selectPlaceholder');
  const searchPlaceholderText = searchPlaceholder || t('common.searchPlaceholder');

  const selectedOption = options.find((opt) => opt.value === value);
  // For allowCustomValue: show the raw value when it doesn't match any option
  const displayText = selectedOption?.label || (allowCustomValue && value ? value : '');

  const filteredOptions = useMemo(() => {
    if (!searchable || !search.trim()) return options;
    const q = search.toLowerCase();
    return options.filter(
      (opt) =>
        opt.label.toLowerCase().includes(q) || opt.value.toLowerCase().includes(q),
    );
  }, [options, search, searchable]);

  // Compute dropdown position relative to viewport
  const updatePosition = () => {
    if (!containerRef.current) return;
    const rect = containerRef.current.getBoundingClientRect();
    setDropdownPos({ top: rect.bottom + 6, left: rect.left, width: rect.width });
  };

  const closeDropdown = useCallback(() => {
    if (allowCustomValue && search.trim()) {
      onChange(search.trim());
    }
    setIsOpen(false);
    setSearch('');
  }, [allowCustomValue, onChange, search]);

  // Close on outside click (check both trigger and portal dropdown)
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      const target = event.target as Node;
      if (
        containerRef.current &&
        !containerRef.current.contains(target) &&
        dropdownRef.current &&
        !dropdownRef.current.contains(target)
      ) {
        closeDropdown();
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [closeDropdown]);

  // Close on escape key
  useEffect(() => {
    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        closeDropdown();
      }
    };

    if (isOpen) {
      document.addEventListener('keydown', handleEscape);
      return () => document.removeEventListener('keydown', handleEscape);
    }
  }, [closeDropdown, isOpen]);

  // Recalculate position while open (scroll / resize)
  useEffect(() => {
    if (!isOpen) return;
    updatePosition();
    const onScrollResize = () => updatePosition();
    window.addEventListener('scroll', onScrollResize, true);
    window.addEventListener('resize', onScrollResize);
    return () => {
      window.removeEventListener('scroll', onScrollResize, true);
      window.removeEventListener('resize', onScrollResize);
    };
  }, [isOpen]);

  // Auto-focus search input when dropdown opens
  useEffect(() => {
    if (isOpen && searchable) {
      // Small delay to let the portal mount
      const id = requestAnimationFrame(() => searchInputRef.current?.focus());
      return () => cancelAnimationFrame(id);
    }
  }, [isOpen, searchable]);

  const handleSelect = (optionValue: string) => {
    onChange(optionValue);
    setIsOpen(false);
    setSearch('');
  };

  const handleSearchKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' && allowCustomValue && search.trim()) {
      // If an exact match exists in filtered options, select that; otherwise commit custom value
      const exact = filteredOptions.find((opt) => opt.value === search.trim());
      onChange(exact ? exact.value : search.trim());
      setIsOpen(false);
      setSearch('');
    }
  };

  const toggleOpen = () => {
    if (disabled) return;
    if (!isOpen) {
      setSearch('');
      updatePosition();
      setIsOpen(true);
      return;
    }
    closeDropdown();
  };

  // Dropdown rendered via Portal
  const dropdown = (
    <AnimatePresence>
      {isOpen && dropdownPos && (
        <motion.div
          ref={dropdownRef}
          initial={{ opacity: 0, y: -8, scale: 0.97 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={{ opacity: 0, y: -8, scale: 0.97 }}
          transition={{ duration: 0.15, ease: 'easeOut' }}
          style={{
            position: 'fixed',
            top: dropdownPos.top,
            left: dropdownPos.left,
            width: Math.max(dropdownPos.width, 180),
            zIndex: 9999,
          }}
          className={cn(
            'rounded-xl border border-zinc-200 dark:border-zinc-700',
            'bg-white dark:bg-zinc-900',
            'shadow-lg shadow-zinc-200/50 dark:shadow-zinc-900/50',
            'overflow-hidden',
          )}
        >
          {/* Search input */}
          {searchable && (
            <div className="flex items-center gap-2 border-b border-zinc-200 px-3 py-2 dark:border-zinc-700">
              <Search className="h-3.5 w-3.5 text-zinc-400" />
              <input
                ref={searchInputRef}
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                onKeyDown={handleSearchKeyDown}
                placeholder={searchPlaceholderText}
                className="flex-1 bg-transparent text-sm text-zinc-900 outline-none placeholder:text-zinc-400 dark:text-zinc-200 dark:placeholder:text-zinc-500"
              />
            </div>
          )}

          <div className="max-h-60 overflow-y-auto p-1">
            {/* Hint: press Enter to use custom value */}
            {allowCustomValue && search.trim() && !filteredOptions.some((o) => o.value === search.trim()) && (
              <div className="px-3 py-1.5 text-xs text-indigo-500 dark:text-indigo-400">
                {t('common.pressEnterCustom', { value: search.trim() })}
              </div>
            )}
            {filteredOptions.length === 0 && !(allowCustomValue && search.trim()) ? (
              <div className="px-3 py-2 text-sm text-zinc-400 dark:text-zinc-500">{t('common.noResults')}</div>
            ) : (
              filteredOptions.map((option) => {
                const isSelected = option.value === value;
                return (
                  <button
                    key={option.value}
                    type="button"
                    disabled={option.disabled}
                    onClick={() => handleSelect(option.value)}
                    className={cn(
                      'w-full flex items-center justify-between gap-2 rounded-lg px-3 py-2 text-sm text-left',
                      'transition-colors duration-150',
                      option.disabled && 'cursor-not-allowed opacity-50',
                      isSelected
                        ? 'bg-indigo-50 text-indigo-600 dark:bg-indigo-500/10 dark:text-indigo-400'
                        : 'text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800',
                    )}
                  >
                    <span className="truncate">{option.labelNode || option.label}</span>
                    {isSelected && <Check className="h-4 w-4 flex-shrink-0" />}
                  </button>
                );
              })
            )}
          </div>
        </motion.div>
      )}
    </AnimatePresence>
  );

  return (
    <div ref={containerRef} className={cn('relative', className)}>
      {/* Trigger button */}
      <button
        type="button"
        onClick={toggleOpen}
        disabled={disabled}
        className={cn(
          'w-full flex items-center justify-between gap-2 rounded-lg border px-3 py-2.5 text-sm',
          'bg-white dark:bg-zinc-900',
          'transition-all duration-200',
          'outline-none',
          disabled && 'cursor-not-allowed opacity-50',
          isOpen
            ? 'border-indigo-500 ring-1 ring-indigo-500'
            : 'border-zinc-200 dark:border-zinc-700 hover:border-zinc-300 dark:hover:border-zinc-600',
          !selectedOption && 'text-zinc-400 dark:text-zinc-500',
        )}
      >
        <span className="truncate">{displayText || placeholderText}</span>
        <ChevronDown
          className={cn(
            'h-4 w-4 text-zinc-400 transition-transform duration-200',
            isOpen && 'rotate-180',
          )}
        />
      </button>

      {/* Dropdown rendered via Portal to escape parent overflow clipping */}
      {typeof window !== 'undefined' && createPortal(dropdown, document.body)}
    </div>
  );
}

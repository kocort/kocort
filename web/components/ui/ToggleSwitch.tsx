interface ToggleSwitchProps {
    checked: boolean;
    onChange: (checked: boolean) => void;
    disabled?: boolean;
}

/**
 * Reusable toggle-switch control matching the project's design language.
 */
export function ToggleSwitch({ checked, onChange, disabled }: ToggleSwitchProps) {
    return (
        <button
            type="button"
            onClick={() => onChange(!checked)}
            disabled={disabled}
            className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${checked ? 'bg-indigo-600' : 'bg-zinc-300 dark:bg-zinc-700'
                }`}
        >
            <span
                className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${checked ? 'translate-x-6' : 'translate-x-1 shadow-sm'
                    }`}
            />
        </button>
    );
}

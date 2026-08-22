export interface StatusPanelProps {
  message: string;
  error: boolean;
}

/**
 * One line of state: loading, ready, or what went wrong.
 *
 * aria-live="polite" so that a screen reader hears the change -- the panel is
 * the only place the page says whether audio started, and nothing moves focus
 * there.
 */
export function StatusPanel({ message, error }: StatusPanelProps) {
  return (
    <p
      className="status-panel"
      data-error={error ? "true" : "false"}
      aria-live="polite"
    >
      {message}
    </p>
  );
}

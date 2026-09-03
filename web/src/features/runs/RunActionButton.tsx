import type { ReactNode } from "react";
import { Loader2 } from "lucide-react";

export interface RunActionButtonProps {
  /** Button text, and the accessible name the tests select on. */
  label: string;
  /** Shown when idle; a spinner replaces it while busy. */
  icon: ReactNode;
  /** Whether the run is in a state where this action applies at all. */
  enabled: boolean;
  onClick: () => void;
  busy: boolean;
  /**
   * Why the action is unavailable to this user. Present only on the fallback
   * a RequireCapability renders, and its presence is what locks the button:
   * a reason and a working button together would be a lie.
   */
  disabledReason?: string;
  /** Test id for the reason paragraph. Each screen names its own. */
  reasonTestID: string;
}

/**
 * One run action: a button that spins while its mutation is in flight, with an
 * optional line underneath saying why it cannot be used.
 *
 * Approve and Cancel were separate components with identical bodies, and
 * Approve was additionally duplicated across RunDetailScreen and
 * TemplateRunActions — three copies of the same fifteen lines, differing only
 * in an icon, a label, and a test id.
 *
 * The bare buttons inside RunControls are deliberately not this component: they
 * sit directly in a flex `.button-row` and share one reason line for the whole
 * row, so wrapping each in this component's `<div>` would change that layout.
 */
export default function RunActionButton({ label, icon, enabled, onClick, busy, disabledReason, reasonTestID }: RunActionButtonProps) {
  const locked = Boolean(disabledReason);
  return (
    <div>
      <button className="secondary-button" disabled={locked || !enabled || busy} onClick={onClick} type="button">
        {busy ? <Loader2 size={16} className="spin" /> : icon}
        {label}
      </button>
      {disabledReason && (
        <p className="muted" data-testid={reasonTestID}>
          {disabledReason}
        </p>
      )}
    </div>
  );
}

import { Loader2, Save } from "lucide-react";
import type { TemplateVariable } from "../../api/types";
import VariableFields from "./VariableFields";

interface StackTemplateConfigPanelProps {
  variables: TemplateVariable[];
  variableValues: Record<string, string>;
  onVariableValueChange: (name: string, value: string) => void;
  canSave: boolean;
  onSave: () => void;
  saveBusy: boolean;
  // When set, the inputs and the action render disabled and the reason is
  // shown — the capability gate's denied-with-reason state (AUTH-020).
  disabledReason?: string;
}

/**
 * Variables for the installed template's desired revision, with a single
 * action. Installing and upgrading live on their own screens, so this panel
 * never has to switch modes.
 */
export default function StackTemplateConfigPanel({
  variables,
  variableValues,
  onVariableValueChange,
  canSave,
  onSave,
  saveBusy,
  disabledReason
}: StackTemplateConfigPanelProps) {
  const locked = Boolean(disabledReason);
  return (
    <section className="panel wide" data-testid="stack-template-config">
      <h2>Variables</h2>
      <VariableFields
        variables={variables}
        variableValues={variableValues}
        onVariableValueChange={onVariableValueChange}
        disabled={locked}
      />
      <div className="button-row form-actions">
        <button className="primary-button" disabled={locked || !canSave || saveBusy} onClick={onSave} type="button">
          {saveBusy ? <Loader2 size={16} className="spin" /> : <Save size={16} />}
          Save config
        </button>
      </div>
      {disabledReason && (
        <p className="muted" data-testid="variables-disabled-reason">
          {disabledReason}
        </p>
      )}
    </section>
  );
}

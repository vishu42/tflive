import { ArrowUpCircle, Loader2, Save, ShieldCheck } from "lucide-react";
import type { TemplateVariable } from "../../api/types";
import StatusRow from "../../shared/StatusRow";

interface VariablesPanelProps {
  variables: TemplateVariable[];
  variableValues: Record<string, string>;
  onVariableValueChange: (name: string, value: string) => void;
  canInstall: boolean;
  onInstall: () => void;
  installBusy: boolean;
  canSaveConfig: boolean;
  onSaveConfig: () => void;
  configBusy: boolean;
  canUpgrade: boolean;
  onUpgrade: () => void;
  upgradeBusy: boolean;
  installedTemplateStatus: string;
  // When set, every input and action renders disabled and the reason is
  // shown — the capability gate's denied-with-reason state (AUTH-020).
  disabledReason?: string;
}

export default function VariablesPanel({
  variables,
  variableValues,
  onVariableValueChange,
  canInstall,
  onInstall,
  installBusy,
  canSaveConfig,
  onSaveConfig,
  configBusy,
  canUpgrade,
  onUpgrade,
  upgradeBusy,
  installedTemplateStatus,
  disabledReason
}: VariablesPanelProps) {
  const locked = Boolean(disabledReason);
  return (
    <section className="panel wide">
      <h2>Variables</h2>
      {variables.length === 0 ? (
        <p className="muted">No variables loaded</p>
      ) : (
        <div className="variable-grid">
          {variables.map((variable) => (
            <label key={variable.name}>
              {variable.name}
              {variable.required ? " *" : ""}
              <input
                value={variableValues[variable.name] ?? ""}
                onChange={(event) => onVariableValueChange(variable.name, event.target.value)}
                placeholder={variable.type_expression || "value"}
                disabled={locked}
              />
            </label>
          ))}
        </div>
      )}
      <div className="button-row form-actions">
        <button className="secondary-button" disabled={locked || !canInstall || installBusy} onClick={onInstall} type="button">
          {installBusy ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
          Install template
        </button>
        <button className="secondary-button" disabled={locked || !canSaveConfig || configBusy} onClick={onSaveConfig} type="button">
          {configBusy ? <Loader2 size={16} className="spin" /> : <Save size={16} />}
          Save config
        </button>
        <button className="primary-button" disabled={locked || !canUpgrade || upgradeBusy} onClick={onUpgrade} type="button">
          {upgradeBusy ? <Loader2 size={16} className="spin" /> : <ArrowUpCircle size={16} />}
          Upgrade
        </button>
      </div>
      {disabledReason && (
        <p className="muted" data-testid="variables-disabled-reason">
          {disabledReason}
        </p>
      )}
      <StatusRow label="Installed template" value={installedTemplateStatus} />
    </section>
  );
}

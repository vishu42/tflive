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
  installedTemplateStatus
}: VariablesPanelProps) {
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
              />
            </label>
          ))}
        </div>
      )}
      <div className="button-row form-actions">
        <button className="secondary-button" disabled={!canInstall || installBusy} onClick={onInstall} type="button">
          {installBusy ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
          Install template
        </button>
        <button className="secondary-button" disabled={!canSaveConfig || configBusy} onClick={onSaveConfig} type="button">
          {configBusy ? <Loader2 size={16} className="spin" /> : <Save size={16} />}
          Save config
        </button>
        <button className="primary-button" disabled={!canUpgrade || upgradeBusy} onClick={onUpgrade} type="button">
          {upgradeBusy ? <Loader2 size={16} className="spin" /> : <ArrowUpCircle size={16} />}
          Upgrade
        </button>
      </div>
      <StatusRow label="Installed template" value={installedTemplateStatus} />
    </section>
  );
}

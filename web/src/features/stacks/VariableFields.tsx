import type { TemplateVariable } from "../../api/types";

interface VariableFieldsProps {
  variables: TemplateVariable[];
  variableValues: Record<string, string>;
  onVariableValueChange: (name: string, value: string) => void;
  disabled?: boolean;
  emptyMessage?: string;
}

/**
 * The variable input grid, and nothing else — no mutations, no revision
 * selection, no actions. Shared by the template, add, and upgrade screens so
 * all three render variables identically.
 */
export default function VariableFields({
  variables,
  variableValues,
  onVariableValueChange,
  disabled = false,
  emptyMessage = "No variables loaded"
}: VariableFieldsProps) {
  if (variables.length === 0) {
    return <p className="muted">{emptyMessage}</p>;
  }
  return (
    <div className="variable-grid">
      {variables.map((variable) => (
        <label key={variable.name}>
          {variable.name}
          {variable.required ? " *" : ""}
          <input
            value={variableValues[variable.name] ?? ""}
            onChange={(event) => onVariableValueChange(variable.name, event.target.value)}
            placeholder={variable.type_expression || "value"}
            disabled={disabled}
          />
        </label>
      ))}
    </div>
  );
}

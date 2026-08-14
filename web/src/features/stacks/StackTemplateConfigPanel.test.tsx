// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TemplateVariable } from "../../api/types";
import StackTemplateConfigPanel from "./StackTemplateConfigPanel";

function variable(overrides: Partial<TemplateVariable> = {}): TemplateVariable {
  return {
    template_revision_id: "rev_1",
    name: "region",
    type_expression: "string",
    description: "",
    required: true,
    has_default: false,
    sensitive: false,
    has_validation: false,
    ...overrides
  };
}

function renderPanel(overrides: Partial<React.ComponentProps<typeof StackTemplateConfigPanel>> = {}) {
  const props = {
    variables: [variable()],
    variableValues: { region: "us-east-1" },
    onVariableValueChange: vi.fn(),
    canSave: true,
    onSave: vi.fn(),
    saveBusy: false,
    ...overrides
  };
  render(<StackTemplateConfigPanel {...props} />);
  return props;
}

describe("StackTemplateConfigPanel", () => {
  afterEach(cleanup);

  it("offers exactly one action", () => {
    renderPanel();

    expect(screen.getAllByRole("button")).toHaveLength(1);
    expect(screen.getByRole("button", { name: /Save config/ })).toBeTruthy();
  });

  it("reports edits to a variable value", () => {
    const props = renderPanel();

    fireEvent.change(screen.getByLabelText(/region/), { target: { value: "eu-west-1" } });

    expect(props.onVariableValueChange).toHaveBeenCalledWith("region", "eu-west-1");
  });

  it("disables save when canSave is false", () => {
    renderPanel({ canSave: false });

    expect((screen.getByRole("button", { name: /Save config/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("locks every input and the action when a disabled reason is given", () => {
    renderPanel({ disabledReason: "Editing requires operator access" });

    expect(screen.getByTestId("variables-disabled-reason").textContent).toContain("Editing requires operator access");
    expect((screen.getByRole("button", { name: /Save config/ }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByLabelText(/region/) as HTMLInputElement).disabled).toBe(true);
  });

  it("renders a message instead of a grid when the revision has no variables", () => {
    renderPanel({ variables: [], variableValues: {} });

    expect(screen.getByText("No variables loaded")).toBeTruthy();
  });
});

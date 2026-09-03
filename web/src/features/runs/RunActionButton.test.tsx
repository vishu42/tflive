// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ShieldCheck } from "lucide-react";
import { afterEach, describe, expect, it, vi } from "vitest";
import RunActionButton from "./RunActionButton";

afterEach(cleanup);

function renderButton(overrides: Partial<Parameters<typeof RunActionButton>[0]> = {}) {
  const onClick = vi.fn();
  render(
    <RunActionButton
      label="Approve"
      icon={<ShieldCheck size={16} />}
      enabled
      onClick={onClick}
      busy={false}
      reasonTestID="approve-disabled-reason"
      {...overrides}
    />
  );
  return { onClick };
}

// The three copies this replaced each carried their own version of the enabled
// rule, so the rule itself is what these cover: three independent things can
// disable the button, and any one of them is enough.
describe("RunActionButton", () => {
  it("invokes onClick when the action is available", () => {
    const { onClick } = renderButton();

    const button = screen.getByRole("button", { name: /approve/i });
    expect(button.hasAttribute("disabled")).toBe(false);
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("disables the button when the run is not in an actionable state", () => {
    renderButton({ enabled: false });

    expect(screen.getByRole("button", { name: /approve/i }).hasAttribute("disabled")).toBe(true);
  });

  it("disables the button while the mutation is in flight", () => {
    renderButton({ busy: true });

    expect(screen.getByRole("button", { name: /approve/i }).hasAttribute("disabled")).toBe(true);
  });

  // A reason and a working button together would tell the user they may do
  // something the server will refuse, so the reason locks the button on its
  // own — even when the run itself is in an actionable state.
  it("locks the button and shows the reason when the user lacks the capability", () => {
    renderButton({ enabled: true, disabledReason: "Approving requires approver access" });

    expect(screen.getByRole("button", { name: /approve/i }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByTestId("approve-disabled-reason").textContent).toBe("Approving requires approver access");
  });

  it("renders no reason paragraph when the user holds the capability", () => {
    renderButton();

    expect(screen.queryByTestId("approve-disabled-reason")).toBeNull();
  });

  it("names the reason paragraph with the test id the calling screen chose", () => {
    renderButton({ disabledReason: "nope", reasonTestID: "template-run-approve-disabled-reason" });

    expect(screen.getByTestId("template-run-approve-disabled-reason")).toBeTruthy();
  });
});

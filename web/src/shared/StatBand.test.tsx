// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import StatBand from "./StatBand";

describe("StatBand", () => {
  it("renders a label and value for each item", () => {
    render(<StatBand items={[{ label: "Stacks", value: 4 }, { label: "Running", value: 1 }]} />);
    expect(screen.getByText("Stacks")).toBeTruthy();
    expect(screen.getByText("4")).toBeTruthy();
    expect(screen.getByText("Running")).toBeTruthy();
    expect(screen.getByText("1")).toBeTruthy();
  });

  it("renders nothing when given no items", () => {
    const { container } = render(<StatBand items={[]} />);
    expect(container.querySelector(".stat-band")).toBeNull();
  });
});

// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import SectionLabel from "./SectionLabel";

describe("SectionLabel", () => {
  it("renders its text", () => {
    render(<SectionLabel>Stacks</SectionLabel>);
    expect(screen.getByText("Stacks")).toBeTruthy();
  });

  it("marks the dot as decorative for screen readers", () => {
    const { container } = render(<SectionLabel pulse>Runs</SectionLabel>);
    const dot = container.querySelector(".section-label__dot");
    expect(dot?.getAttribute("aria-hidden")).toBe("true");
  });
});

// @vitest-environment jsdom
import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import Breadcrumb from "./Breadcrumb";

describe("Breadcrumb", () => {
  it("links each ancestor and makes the current page the h1", () => {
    render(
      <MemoryRouter>
        <Breadcrumb items={[{ label: "Templates", to: "/templates" }, { label: "vpc" }]} detail="acme/vpc · main" />
      </MemoryRouter>
    );

    const nav = screen.getByRole("navigation", { name: "Breadcrumb" });
    expect(within(nav).getByRole("link", { name: "Templates" }).getAttribute("href")).toBe("/templates");
    const heading = within(nav).getByRole("heading", { level: 1 });
    expect(heading.textContent).toBe("vpc");
    expect(heading.getAttribute("aria-current")).toBe("page");
    expect(within(nav).getAllByRole("listitem")).toHaveLength(2);
    expect(within(nav).getByText("acme/vpc · main")).toBeTruthy();
  });
});

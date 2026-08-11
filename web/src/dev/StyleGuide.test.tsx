// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import StyleGuide from "./StyleGuide";

afterEach(cleanup);

describe("StyleGuide", () => {
  it("renders without a backend, an auth provider, or a router", () => {
    // The whole point of this page is that it mounts standalone: the app
    // itself cannot render without Keycloak, so anything this gallery needed
    // from context would make it useless for viewing the design system.
    render(<StyleGuide />);
    expect(screen.getByTestId("styleguide")).toBeTruthy();
  });

  it("renders the real status tones rather than copies of their markup", () => {
    const { container } = render(<StyleGuide />);
    // If StatusRow's own class contract changed, this breaks — which is the
    // point: the gallery must not be able to drift from the components.
    for (const tone of ["settled", "progress", "waiting", "failed", "canceled"]) {
      expect(container.querySelector(`.status-tone--${tone}`), `missing tone ${tone}`).toBeTruthy();
    }
  });

  it("shows every role badge variant", () => {
    const { container } = render(<StyleGuide />);
    for (const role of ["owner", "operator", "approver", "viewer"]) {
      expect(container.querySelector(`.role-badge--${role}`), `missing role ${role}`).toBeTruthy();
    }
  });

  it("links to every section it documents", () => {
    const { container } = render(<StyleGuide />);
    const links = Array.from(container.querySelectorAll(".sg__nav a")).map((a) => a.getAttribute("href"));
    expect(links.length).toBeGreaterThan(0);
    for (const href of links) {
      const id = href?.replace("#", "") ?? "";
      expect(container.querySelector(`#${id}`), `nav links to missing section #${id}`).toBeTruthy();
    }
  });
});

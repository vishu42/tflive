import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import AccessDenied from "./AccessDenied";

describe("AccessDenied", () => {
  it("renders a not-permitted message", () => {
    const markup = renderToStaticMarkup(<AccessDenied />);

    expect(markup).toContain('data-testid="route-access-denied"');
    expect(markup).toContain("Not permitted");
  });
});

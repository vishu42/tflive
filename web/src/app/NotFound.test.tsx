import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import NotFound from "./NotFound";

describe("NotFound", () => {
  it("renders a 404 message", () => {
    const markup = renderToStaticMarkup(<NotFound />);

    expect(markup).toContain('data-testid="route-not-found"');
    expect(markup).toContain("Page not found");
  });
});

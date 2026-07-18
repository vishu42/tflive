import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import RoutePlaceholder from "./RoutePlaceholder";

describe("RoutePlaceholder", () => {
  it("renders the given title inside a placeholder region", () => {
    const markup = renderToStaticMarkup(<RoutePlaceholder title="Stacks" />);

    expect(markup).toContain('data-testid="route-placeholder"');
    expect(markup).toContain("Stacks");
    expect(markup).toContain("This screen has not been built yet.");
  });
});

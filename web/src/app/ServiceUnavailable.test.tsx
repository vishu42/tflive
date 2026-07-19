import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import ServiceUnavailable from "./ServiceUnavailable";

describe("ServiceUnavailable", () => {
  it("renders the authorization-service-unavailable banner", () => {
    const markup = renderToStaticMarkup(<ServiceUnavailable />);

    expect(markup).toContain('data-testid="route-service-unavailable"');
    expect(markup).toContain("Authorization service unavailable — try again shortly.");
  });
});

// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import CallbackPage from "./CallbackPage";

describe("CallbackPage", () => {
  it("renders a signing-in indicator", () => {
    const markup = renderToStaticMarkup(<CallbackPage />);

    expect(markup).toContain("Signing in");
  });
});

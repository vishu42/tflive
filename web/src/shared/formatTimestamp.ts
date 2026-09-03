// The first date formatting in the app.
//
// Absolute rather than relative: "2 days ago" needs a clock, which makes every
// test that renders one depend on when it runs. The locale is pinned so the
// rendered string does not change with the runner's or the viewer's locale
// either — a fixed format is the point, not localisation.
const FORMATTER = new Intl.DateTimeFormat("en-GB", {
  day: "numeric",
  month: "short",
  year: "numeric"
});

export function formatTimestamp(isoTimestamp: string): string {
  const parsed = new Date(isoTimestamp);
  // An unparseable value yields NaN, and formatting that throws a RangeError.
  // Showing the raw string beats taking the screen down over a bad timestamp.
  if (Number.isNaN(parsed.getTime())) {
    return isoTimestamp;
  }
  return FORMATTER.format(parsed);
}

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

const DATE_TIME_FORMATTER = new Intl.DateTimeFormat("en-GB", {
  day: "numeric",
  month: "short",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23"
});

// Day, month and time without the year, for events that are usually recent,
// such as runs: "21 Sep, 05:21". In the viewer's time zone; render it inside a
// <time dateTime> so the exact instant is still there to read.
export function formatDateTime(isoTimestamp: string): string {
  const parsed = new Date(isoTimestamp);
  if (Number.isNaN(parsed.getTime())) {
    return isoTimestamp;
  }
  return DATE_TIME_FORMATTER.format(parsed);
}

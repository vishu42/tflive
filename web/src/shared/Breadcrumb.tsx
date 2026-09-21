import type { ReactNode } from "react";
import { Link } from "react-router-dom";

export type Crumb = {
  label: ReactNode;
  /** Omitted on the last crumb, which is the current page. */
  to?: string;
  testId?: string;
};

// The page's title bar. The last crumb is the page's h1, so every screen keeps
// exactly one top-level heading even though nothing on screen looks like one.
// Separators are drawn in CSS so assistive tech counts only real crumbs.
export default function Breadcrumb({ items, detail }: { items: Crumb[]; detail?: ReactNode }) {
  const current = items[items.length - 1];
  const trail = items.slice(0, -1);

  return (
    <nav className="breadcrumb" aria-label="Breadcrumb">
      <ol>
        {trail.map((crumb, index) => (
          <li key={index}>
            {crumb.to ? (
              <Link to={crumb.to} data-testid={crumb.testId}>
                {crumb.label}
              </Link>
            ) : (
              <span data-testid={crumb.testId}>{crumb.label}</span>
            )}
          </li>
        ))}
        <li>
          <h1 aria-current="page" data-testid={current.testId}>
            {current.label}
          </h1>
        </li>
      </ol>
      {detail && <span className="breadcrumb__detail">{detail}</span>}
    </nav>
  );
}

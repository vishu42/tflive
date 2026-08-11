import type { ReactNode } from "react";

export default function SectionLabel({
  children,
  pulse = false
}: {
  children: ReactNode;
  pulse?: boolean;
}) {
  return (
    <span className="section-label">
      <span
        className={`section-label__dot${pulse ? " section-label__dot--pulse" : ""}`}
        aria-hidden="true"
      />
      {children}
    </span>
  );
}

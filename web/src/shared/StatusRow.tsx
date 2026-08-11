import { statusGlyph, statusTone } from "./statusTone";

export default function StatusRow({ label, value }: { label: string; value: string }) {
  const tone = statusTone(value);

  return (
    <div className="status-row" data-status={value}>
      <span>{label}</span>
      <strong>
        <span className={`status-tone status-tone--${tone}`}>
          <span className="status-tone__glyph" aria-hidden="true">
            {statusGlyph(tone)}
          </span>
          {value}
        </span>
      </strong>
    </div>
  );
}

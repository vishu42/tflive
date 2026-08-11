export type Stat = { label: string; value: string | number };

export default function StatBand({ items }: { items: Stat[] }) {
  if (items.length === 0) return null;

  return (
    <section className="stat-band panel panel--inverted" data-testid="stat-band">
      <dl className="stat-band__grid">
        {items.map((item) => (
          <div key={item.label} className="stat-band__item">
            <dd className="stat-band__value">{item.value}</dd>
            <dt className="stat-band__label">{item.label}</dt>
          </div>
        ))}
      </dl>
    </section>
  );
}

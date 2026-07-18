export default function RoutePlaceholder({ title }: { title: string }) {
  return (
    <section className="route-placeholder" data-testid="route-placeholder">
      <h1>{title}</h1>
      <p className="muted">This screen has not been built yet.</p>
    </section>
  );
}

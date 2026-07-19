export default function ServiceUnavailable() {
  return (
    <section className="route-service-unavailable" data-testid="route-service-unavailable">
      <h1>Authorization service unavailable</h1>
      <p className="muted">Authorization service unavailable — try again shortly.</p>
    </section>
  );
}

export default function AccessDenied() {
  return (
    <section className="route-access-denied" data-testid="route-access-denied">
      <h1>Not permitted</h1>
      <p className="muted">You don't have permission to do this.</p>
    </section>
  );
}

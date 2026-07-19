interface IdsPanelProps {
  registrationID: string;
  templateRevisionID: string;
  stackID: string;
  stackTemplateID: string;
  desiredRevisionID: string;
  appliedRevisionID: string;
  planRunID: string;
  applyRunID: string;
}

export default function IdsPanel({
  registrationID,
  templateRevisionID,
  stackID,
  stackTemplateID,
  desiredRevisionID,
  appliedRevisionID,
  planRunID,
  applyRunID
}: IdsPanelProps) {
  return (
    <section className="panel wide">
      <h2>IDs</h2>
      <dl className="id-grid">
        <dt>Registration</dt>
        <dd>{registrationID}</dd>
        <dt>Template revision</dt>
        <dd>{templateRevisionID}</dd>
        <dt>Stack</dt>
        <dd>{stackID}</dd>
        <dt>Stack template</dt>
        <dd>{stackTemplateID}</dd>
        <dt>Desired revision</dt>
        <dd>{desiredRevisionID}</dd>
        <dt>Applied revision</dt>
        <dd>{appliedRevisionID}</dd>
        <dt>Plan run</dt>
        <dd>{planRunID}</dd>
        <dt>Apply run</dt>
        <dd>{applyRunID}</dd>
      </dl>
    </section>
  );
}

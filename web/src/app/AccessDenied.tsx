import HeroGraphic from "../shared/HeroGraphic";
import SectionLabel from "../shared/SectionLabel";

export default function AccessDenied() {
  return (
    <section className="route-access-denied showcase" data-testid="route-access-denied">
      <div className="showcase__body">
        <SectionLabel>Access</SectionLabel>
        <h1 className="showcase__title gradient-text">Not permitted</h1>
        <p className="muted showcase__lede">You don't have permission to do this.</p>
      </div>
      <div className="showcase__visual">
        <HeroGraphic />
      </div>
    </section>
  );
}

import HeroGraphic from "../shared/HeroGraphic";
import SectionLabel from "../shared/SectionLabel";

export default function RoutePlaceholder({ title }: { title: string }) {
  return (
    <section className="route-placeholder showcase" data-testid="route-placeholder">
      <div className="showcase__body">
        <SectionLabel>Coming soon</SectionLabel>
        <h1 className="showcase__title gradient-text">{title}</h1>
        <p className="muted showcase__lede">This screen has not been built yet.</p>
      </div>
      <div className="showcase__visual">
        <HeroGraphic />
      </div>
    </section>
  );
}

import HeroGraphic from "../shared/HeroGraphic";
import SectionLabel from "../shared/SectionLabel";

export default function NotFound() {
  return (
    <section className="route-not-found showcase" data-testid="route-not-found">
      <div className="showcase__body">
        <SectionLabel>Error 404</SectionLabel>
        <h1 className="showcase__title gradient-text">Page not found</h1>
        <p className="muted showcase__lede">The page you were looking for doesn't exist.</p>
      </div>
      <div className="showcase__visual">
        <HeroGraphic />
      </div>
    </section>
  );
}

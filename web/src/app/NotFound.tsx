import HeroGraphic from "../shared/HeroGraphic";
import { useInView } from "../shared/useInView";

export default function NotFound() {
  const { ref, visible } = useInView<HTMLDivElement>();
  return (
    <section className="route-not-found showcase" data-testid="route-not-found">
      <div className="showcase__body reveal" ref={ref} data-visible={visible}>
        <h1 className="showcase__title gradient-text">Page not found</h1>
        <p className="muted showcase__lede">The page you were looking for doesn't exist.</p>
      </div>
      <div className="showcase__visual">
        <HeroGraphic />
      </div>
    </section>
  );
}

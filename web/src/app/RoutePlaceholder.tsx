import HeroGraphic from "../shared/HeroGraphic";
import { useInView } from "../shared/useInView";

export default function RoutePlaceholder({ title }: { title: string }) {
  const { ref, visible } = useInView<HTMLDivElement>();
  return (
    <section className="route-placeholder showcase" data-testid="route-placeholder">
      <div className="showcase__body reveal" ref={ref} data-visible={visible}>
        <h1 className="showcase__title gradient-text">{title}</h1>
        <p className="muted showcase__lede">This screen has not been built yet.</p>
      </div>
      <div className="showcase__visual">
        <HeroGraphic />
      </div>
    </section>
  );
}

import HeroGraphic from "../shared/HeroGraphic";
import { useInView } from "../shared/useInView";

export default function AccessDenied() {
  const { ref, visible } = useInView<HTMLDivElement>();
  return (
    <section className="route-access-denied showcase" data-testid="route-access-denied">
      <div className="showcase__body reveal" ref={ref} data-visible={visible}>
        <h1 className="showcase__title gradient-text">Not permitted</h1>
        <p className="muted showcase__lede">You don't have permission to do this.</p>
      </div>
      <div className="showcase__visual">
        <HeroGraphic />
      </div>
    </section>
  );
}

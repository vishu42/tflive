import HeroGraphic from "../shared/HeroGraphic";
import { useInView } from "../shared/useInView";

export default function ServiceUnavailable() {
  const { ref, visible } = useInView<HTMLDivElement>();
  return (
    <section className="route-service-unavailable showcase" data-testid="route-service-unavailable">
      <div className="showcase__body reveal" ref={ref} data-visible={visible}>
        <h1 className="showcase__title gradient-text">Authorization service unavailable</h1>
        <p className="muted showcase__lede">Authorization service unavailable — try again shortly.</p>
      </div>
      <div className="showcase__visual">
        <HeroGraphic />
      </div>
    </section>
  );
}

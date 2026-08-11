/**
 * Decorative showpiece. Pure CSS and inline SVG — no data, no props, no JS
 * animation. Hidden from assistive technology entirely.
 */
export default function HeroGraphic() {
  return (
    <div className="hero-graphic" aria-hidden="true">
      <svg className="hero-graphic__ring" viewBox="0 0 200 200" role="presentation">
        <circle
          cx="100"
          cy="100"
          r="92"
          fill="none"
          stroke="currentColor"
          strokeWidth="1"
          strokeDasharray="4 8"
        />
      </svg>

      <div className="hero-graphic__blob" />

      <div className="hero-graphic__card hero-graphic__card--one">
        <span className="hero-graphic__bar" />
        <span className="hero-graphic__bar hero-graphic__bar--short" />
      </div>

      <div className="hero-graphic__card hero-graphic__card--two">
        <span className="hero-graphic__bar hero-graphic__bar--short" />
        <span className="hero-graphic__bar" />
      </div>

      <div className="hero-graphic__dots">
        {Array.from({ length: 9 }, (_, i) => (
          <span key={i} />
        ))}
      </div>

      <div className="hero-graphic__corner" />
    </div>
  );
}

import { useCallback, useEffect, useState } from "react";

/**
 * Fires once when the element first enters the viewport, then disconnects.
 * Thresholds match the design system's viewport options: 15% visible, with a
 * 60px bottom margin so content settles before it animates.
 */
export function useInView<T extends Element>() {
  const [node, setNode] = useState<T | null>(null);
  const [visible, setVisible] = useState(false);
  const ref = useCallback((element: T | null) => {
    setNode(element);
  }, []);

  useEffect(() => {
    if (!node || visible) return;

    // Environments without IntersectionObserver (older jsdom, SSR) should show
    // content rather than hide it forever.
    if (typeof IntersectionObserver === "undefined") {
      setVisible(true);
      return;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setVisible(true);
          observer.disconnect();
        }
      },
      { threshold: 0.15, rootMargin: "0px 0px -60px 0px" }
    );

    observer.observe(node);
    return () => observer.disconnect();
  }, [node, visible]);

  return { ref, visible };
}

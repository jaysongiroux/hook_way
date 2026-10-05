import { useEffect, useRef, useState } from "react";

/** Pause decorative loops while their surface or browser tab is hidden. */
export function useVisibleMotion() {
  const viewportRef = useRef<HTMLDivElement>(null);
  const [inView, setInView] = useState(true);
  const [pageVisible, setPageVisible] = useState(
    () => typeof document === "undefined" || !document.hidden,
  );

  useEffect(() => {
    const onVisibilityChange = () => setPageVisible(!document.hidden);
    document.addEventListener("visibilitychange", onVisibilityChange);
    const target = viewportRef.current;
    const observer = target && typeof IntersectionObserver !== "undefined"
      ? new IntersectionObserver(([entry]) => setInView(entry.isIntersecting))
      : undefined;
    if (target) observer?.observe(target);
    return () => {
      document.removeEventListener("visibilitychange", onVisibilityChange);
      observer?.disconnect();
    };
  }, []);

  return { viewportRef, running: inView && pageVisible };
}

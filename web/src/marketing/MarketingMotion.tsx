import { useReducedMotion } from "@mantine/hooks";
import { useAnimate } from "motion/react-mini";
import { useLayoutEffect, type ReactNode } from "react";

const ARRIVE = [0.22, 1, 0.36, 1] as const;

/** Opt-in marketing motion; docs share the shell but never enable this. */
export function MarketingMotion({
  children,
  enabled,
}: {
  children: ReactNode;
  enabled: boolean;
}) {
  const [scope, animate] = useAnimate<HTMLElement>();
  const reducedMotion = useReducedMotion(undefined, {
    getInitialValueInEffect: false,
  });

  useLayoutEffect(() => {
    if (
      !enabled ||
      reducedMotion ||
      !scope.current ||
      typeof Element.prototype.animate !== "function" ||
      typeof IntersectionObserver === "undefined"
    )
      return;

    const root = scope.current;
    const groups = new Map<HTMLElement, ReturnType<typeof animate>[]>();
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (!entry.isIntersecting) continue;
          observer.unobserve(entry.target);
          groups
            .get(entry.target as HTMLElement)
            ?.forEach((animation) => animation.play());
        }
      },
      { threshold: 0, rootMargin: "0px 0px -24px 0px" },
    );

    // Prepare before paint, then play on entry. Starting a new animation only
    // after intersection makes already-visible text jump back to its first frame.
    // Styles stay visible by default if JS, WAAPI or reduced motion disables this.
    root
      .querySelectorAll<HTMLElement>("[data-marketing-motion]")
      .forEach((element) => {
        const kind = element.dataset.marketingMotion;
        const isSequence = kind === "sequence";
        const isIntro = kind === "intro";
        const isVisual = kind === "visual";
        const nodes = isSequence
          ? Array.from(element.querySelectorAll<HTMLElement>(":scope > li"))
          : isIntro
            ? (Array.from(element.children) as HTMLElement[])
            : [element];
        const animations = nodes.map((node, index) =>
          animate(
            node,
            {
              transform: isVisual
                ? ["translateY(16px)", "translateY(0px)"]
                : isSequence
                  ? ["translateX(-12px)", "translateX(0px)"]
                  : [`translateY(${isIntro ? 16 : 12}px)`, "translateY(0px)"],
            },
            {
              duration: isIntro || isVisual ? 0.8 : 0.6,
              delay: isVisual
                ? 0.16
                : Math.min(index * (isSequence ? 0.18 : 0.1), 0.54),
              ease: ARRIVE,
              autoplay: false,
            },
          ),
        );
        groups.set(element, animations);
        observer.observe(element);
      });

    // Keyboard navigation must never land in a pending entrance animation.
    const revealFocused = (event: FocusEvent) => {
      // Pointer focus happens between mouse-down and mouse-up. Finishing a
      // transform there can move the link out from under the click.
      if (
        !(event.target instanceof Element) ||
        !event.target.matches(":focus-visible")
      )
        return;
      for (const [element, animations] of groups) {
        if (event.target instanceof Node && element.contains(event.target)) {
          observer.unobserve(element);
          animations.forEach((animation) => animation.complete());
        }
      }
    };
    root.addEventListener("focusin", revealFocused);
    return () => {
      observer.disconnect();
      root.removeEventListener("focusin", revealFocused);
      groups.forEach((animations) =>
        animations.forEach((animation) => animation.cancel()),
      );
    };
  }, [enabled, reducedMotion, scope, animate]);

  return <main ref={scope}>{children}</main>;
}

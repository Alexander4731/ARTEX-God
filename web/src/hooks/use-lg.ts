import { listenMedia } from "@/lib/media-query";
import * as React from "react";

const LG_BREAKPOINT = 1024;

export function useIsLg() {
  const [isLg, setIsLg] = React.useState<boolean | undefined>(undefined);

  React.useEffect(() => {
    const onChange = () => setIsLg(window.innerWidth >= LG_BREAKPOINT);
    onChange();
    if (typeof window.matchMedia !== "function") {
      window.addEventListener("resize", onChange);
      return () => window.removeEventListener("resize", onChange);
    }
    return listenMedia(window.matchMedia(`(min-width: ${LG_BREAKPOINT}px)`), onChange);
  }, []);

  return !!isLg;
}

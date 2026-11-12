import { listenMedia } from "@/lib/media-query";
import * as React from "react";

const MOBILE_BREAKPOINT = 768;

export function useIsMobile() {
  const [isMobile, setIsMobile] = React.useState<boolean | undefined>(undefined);

  React.useEffect(() => {
    const onChange = () => setIsMobile(window.innerWidth < MOBILE_BREAKPOINT);
    onChange();
    if (typeof window.matchMedia !== "function") {
      window.addEventListener("resize", onChange);
      return () => window.removeEventListener("resize", onChange);
    }
    return listenMedia(window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`), onChange);
  }, []);

  return !!isMobile;
}

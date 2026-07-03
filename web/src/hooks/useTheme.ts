import { useEffect, useState } from "react";

export type Theme = "light" | "dark";

export function useTheme(): [Theme, (next: Theme | ((current: Theme) => Theme)) => void] {
  const [theme, setTheme] = useState<Theme>(() => preferredTheme());

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
    window.localStorage.setItem("nexus-lens-theme", theme);
  }, [theme]);

  return [theme, setTheme];
}

function preferredTheme(): Theme {
  const stored = window.localStorage.getItem("nexus-lens-theme");
  if (stored === "dark" || stored === "light") {
    return stored;
  }
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

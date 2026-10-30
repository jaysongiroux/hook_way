/// <reference types="vite/client" />

declare module "virtual:hookway-docs" {
  export const site: { description: string; origin: string };
  const links: Record<string, string>;
  export default links;
}

declare module "virtual:hookway-docs-data" {
  interface NavItem { label: string; link?: string; items?: NavItem[] }
  const publication: {
    sidebar: NavItem[];
    pages: { route: string; title: string; dataUrl: string; source: string | null; rawUrl: string | null }[];
    origin: string;
  };
  export default publication;
}

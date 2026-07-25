// iconify-icon is a custom element registered at runtime by the iconify-icon
// script tag the Go server injects (see cmd/scrapeui/server.go), so TypeScript
// only learns its JSX shape from this declaration.
import 'preact';

declare module 'preact' {
  namespace JSX {
    interface IntrinsicElements {
      'iconify-icon': JSX.HTMLAttributes<HTMLElement> & {
        icon: string;
        width?: string | number;
        height?: string | number;
        inline?: boolean;
      };
    }
  }
}

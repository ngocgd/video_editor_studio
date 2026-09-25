import { Toaster as Sonner } from "sonner";

/**
 * Toasts are for background completions only (guidelines: "Toasts only for
 * background completions"), never for errors that belong inline next to the
 * failing object.
 */
export function Toaster() {
  return (
    <Sonner
      theme="dark"
      position="bottom-right"
      toastOptions={{
        classNames: {
          toast: "!bg-popover !border !border-border !text-foreground !shadow-[var(--shadow-overlay)]",
          title: "!text-foreground",
          description: "!text-muted-foreground",
        },
      }}
    />
  );
}

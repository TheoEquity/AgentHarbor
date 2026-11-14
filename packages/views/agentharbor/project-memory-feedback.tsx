import type { ReactNode } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../i18n";

export function MemoryMessage({ children }: { children: ReactNode }) {
  return <p className="py-10 text-center text-body text-muted-foreground">{children}</p>;
}

export function MemoryLoadError({ message, retry }: { message?: string; retry: () => void }) {
  const { t } = useT("layout");
  return (
    <div role="alert" className="flex flex-wrap items-center gap-2 text-body text-destructive">
      <span>{message ?? t(($) => $.project_memory.load_error)}</span>
      <Button type="button" variant="ghost" size="sm" onClick={retry}>{t(($) => $.agent_tasks.retry)}</Button>
    </div>
  );
}

"use client";

import { useEffect, useRef } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import { Spinner } from "@multica/ui/components/ui/spinner";
import { copyText } from "@multica/ui/lib/clipboard";
import { useT } from "../i18n";
import { useAppOrigin } from "../navigation";
import { isDesktopShell } from "../platform/local-directory";
import { openExternal } from "../platform/open-external";

const HELPER_FILENAME = "connect-codex-memory.ps1";
const HELPER_PATH = `/agentharbor/${HELPER_FILENAME}`;

function downloadHelper(href: string, desktop: boolean) {
  if (desktop) {
    openExternal(href);
    return;
  }
  const anchor = document.createElement("a");
  anchor.href = href;
  anchor.download = HELPER_FILENAME;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
}

export function ProjectMemoryConnection({ projectId }: { projectId: string }) {
  const { t } = useT("layout");
  const appOrigin = useAppOrigin();
  const desktop = isDesktopShell();
  const helperUrl = desktop ? appOrigin && `${appOrigin}${HELPER_PATH}` : HELPER_PATH;
  const active = useRef(false);
  const pendingBundle = useRef<{ text: string; expiresAt: number } | null>(null);

  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
      pendingBundle.current = null;
    };
  }, []);

  const connection = useMutation({
    mutationFn: async () => {
      let bundle = pendingBundle.current;
      if (!bundle || bundle.expiresAt <= Date.now()) {
        const apiUrl = new URL(api.getBaseUrl() || window.location.origin, window.location.href);
        if (apiUrl.protocol !== "http:" && apiUrl.protocol !== "https:") throw new Error("Invalid API URL");
        const issued = await api.issueAgentHarborMemoryToken(projectId);
        if (!active.current) return;
        if (issued.projectId !== projectId) throw new Error("Wrong project");
        bundle = {
          text: JSON.stringify({
            version: 1,
            api_url: apiUrl.href.replace(/\/$/, ""),
            project_id: issued.projectId,
            token: issued.token,
            expires_at: issued.expiresAt,
          }),
          expiresAt: Date.parse(issued.expiresAt),
        };
        // A second click can retry clipboard access with a fresh user gesture.
        pendingBundle.current = bundle;
      }
      if (!(await copyText(bundle.text))) return "clipboard_error";
      pendingBundle.current = null;
      if (!active.current || !helperUrl) return;
      downloadHelper(helperUrl, desktop);
      return "copied";
    },
  });

  return (
    <section className="space-y-3 rounded-xl border border-surface-border bg-card p-4">
      <h2 className="font-medium">{t(($) => $.project_memory.connect_codex)}</h2>
      {helperUrl ? (
        <>
          <p className="text-caption text-muted-foreground">{t(($) => $.project_memory.connect_note)}</p>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={connection.isPending}
            aria-busy={connection.isPending}
            onClick={() => connection.mutate()}
          >
            {connection.isPending && <Spinner aria-hidden />}
            {t(($) => $.project_memory.prepare_connection)}
          </Button>
          {connection.isError && <p role="alert" className="text-caption text-destructive">{t(($) => $.project_memory.connection_error)}</p>}
          {connection.data === "clipboard_error" && <p role="alert" className="text-caption text-destructive">{t(($) => $.project_memory.clipboard_error)}</p>}
          {connection.data === "copied" && (
            <div className="space-y-2 text-caption text-muted-foreground">
              <p role="status">{t(($) => $.project_memory.connection_copied)}</p>
              <p>{t(($) => $.project_memory.run_connection_helper, { filename: HELPER_FILENAME })}</p>
              <a
                href={helperUrl}
                download={desktop ? undefined : HELPER_FILENAME}
                target={desktop ? "_blank" : undefined}
                rel="noreferrer"
                className="inline-block text-primary underline underline-offset-4"
              >
                {t(($) => $.project_memory.redownload_helper)}
              </a>
              <details>
                <summary className="cursor-pointer">{t(($) => $.project_memory.helper_help)}</summary>
                <p className="mt-1">{t(($) => $.project_memory.helper_help_note)}</p>
                <p className="mt-1">{t(($) => $.project_memory.helper_terminal_note)}</p>
                <code className="mt-1 block overflow-x-auto rounded-md bg-muted px-3 py-2">{`& '.\\${HELPER_FILENAME}'`}</code>
              </details>
            </div>
          )}
        </>
      ) : (
        <p className="text-caption text-muted-foreground">{t(($) => $.project_memory.connect_on_web)}</p>
      )}
    </section>
  );
}

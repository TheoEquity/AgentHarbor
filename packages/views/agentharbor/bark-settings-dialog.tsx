"use client";

import { useId, useRef, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import type { AgentHarborBarkUpdate } from "@multica/core/api/schemas";
import { agentHarborBarkOptions } from "@multica/core/agentharbor/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Spinner } from "@multica/ui/components/ui/spinner";
import { Switch } from "@multica/ui/components/ui/switch";
import { useT } from "../i18n";

function isHttpsUrl(value: string) {
  try {
    const url = new URL(value);
    return url.protocol === "https:" && !url.username && !url.password;
  } catch {
    return false;
  }
}

export function BarkSettingsDialog({ wsId }: { wsId: string }) {
  const { t } = useT("layout");
  const id = useId();
  const [open, setOpen] = useState(false);
  const [draftEnabled, setDraftEnabled] = useState<boolean>();
  const [feedback, setFeedback] = useState<"saved" | "test_sent">();
  const [validation, setValidation] = useState<"credential_pair" | "invalid_url">();
  const urlInput = useRef<HTMLInputElement>(null);
  const keyInput = useRef<HTMLInputElement>(null);
  const nextUpdate = useRef<AgentHarborBarkUpdate | null>(null);
  const queryClient = useQueryClient();
  const options = agentHarborBarkOptions(wsId);
  const settings = useQuery({ ...options, enabled: open });
  const enabled = draftEnabled ?? settings.data?.enabled ?? false;
  const configured = settings.data?.configured === true;

  function credentials() {
    return {
      serverUrl: urlInput.current?.value.trim() ?? "",
      deviceKey: keyInput.current?.value.trim() ?? "",
    };
  }

  function clearCredentials() {
    if (urlInput.current) urlInput.current.value = "";
    if (keyInput.current) keyInput.current.value = "";
  }

  const save = useMutation({
    // Keep credentials out of mutation variables, which TanStack Query retains.
    mutationFn: async () => {
      const update = nextUpdate.current;
      nextUpdate.current = null;
      if (!update) throw new Error("Missing Bark settings submission");
      return api.putAgentHarborBark(update, wsId);
    },
    networkMode: "always",
    retry: false,
    onSuccess: async (result) => {
      await queryClient.cancelQueries({ queryKey: options.queryKey });
      queryClient.setQueryData(options.queryKey, result);
      setDraftEnabled(undefined);
      clearCredentials();
      setFeedback("saved");
    },
  });
  const test = useMutation({
    mutationFn: () => api.testAgentHarborBark(wsId),
    networkMode: "always",
    retry: false,
    onSuccess: () => setFeedback("test_sent"),
  });
  const pending = save.isPending || test.isPending;

  function clearFeedback() {
    save.reset();
    test.reset();
    setFeedback(undefined);
    setValidation(undefined);
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending || !settings.data) return;
    clearFeedback();
    const { serverUrl, deviceKey } = credentials();
    if (Boolean(serverUrl) !== Boolean(deviceKey) || (enabled && !serverUrl && !configured)) {
      setValidation("credential_pair");
      return;
    }
    if (serverUrl && !isHttpsUrl(serverUrl)) {
      setValidation("invalid_url");
      return;
    }
    nextUpdate.current = serverUrl ? { enabled, serverUrl, deviceKey } : { enabled };
    save.mutate();
  }

  return (
    <Dialog open={open} onOpenChange={(next, event) => {
      if (pending) {
        event.cancel();
        return;
      }
      setOpen(next);
      if (!next) {
        setDraftEnabled(undefined);
        clearCredentials();
        clearFeedback();
      }
    }}>
      <DialogTrigger render={<Button type="button" variant="ghost" size="sm" />}>
        {t(($) => $.agent_tasks.bark.settings)}
      </DialogTrigger>
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{t(($) => $.agent_tasks.bark.settings)}</DialogTitle>
          <DialogDescription>{t(($) => $.agent_tasks.bark.test_note)}</DialogDescription>
        </DialogHeader>
        {settings.isPending && <p role="status" className="text-muted-foreground">{t(($) => $.agent_tasks.bark.loading)}</p>}
        {settings.isError && (
          <div role="alert" className="flex items-center gap-2 text-destructive">
            <span>{t(($) => $.agent_tasks.bark.load_error)}</span>
            <Button type="button" variant="ghost" size="sm" onClick={() => void settings.refetch()} disabled={settings.isFetching}>
              {t(($) => $.agent_tasks.retry)}
            </Button>
          </div>
        )}
        {settings.data && (
          <form className="space-y-4" onSubmit={submit} autoComplete="off" id={`${id}-form`}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <label htmlFor={`${id}-enabled`} className="font-medium">{t(($) => $.agent_tasks.bark.enabled)}</label>
              <Switch id={`${id}-enabled`} checked={enabled} disabled={pending} onCheckedChange={setDraftEnabled} />
            </div>
            <div className="space-y-1">
              <p id={`${id}-credentials`} className="text-caption text-muted-foreground">
                {configured ? t(($) => $.agent_tasks.bark.keep_credentials) : t(($) => $.agent_tasks.bark.unconfigured)}
              </p>
              <label htmlFor={`${id}-url`} className="block font-medium">{t(($) => $.agent_tasks.bark.server_url)}</label>
              <Input ref={urlInput} id={`${id}-url`} type="password" inputMode="url" autoComplete="off"
                spellCheck={false} disabled={pending} aria-describedby={`${id}-credentials`} />
            </div>
            <div className="space-y-1">
              <label htmlFor={`${id}-key`} className="block font-medium">{t(($) => $.agent_tasks.bark.device_key)}</label>
              <Input ref={keyInput} id={`${id}-key`} type="password" autoComplete="new-password"
                spellCheck={false} disabled={pending} aria-describedby={`${id}-credentials`} />
            </div>
            {validation && <p role="alert" className="text-destructive">{t(($) => $.agent_tasks.bark[validation])}</p>}
            {save.isError && <p role="alert" className="text-destructive">{t(($) => $.agent_tasks.bark.save_error)}</p>}
            {test.isError && <p role="alert" className="text-destructive">{t(($) => $.agent_tasks.bark.test_error)}</p>}
            {feedback && <p role="status" className="text-muted-foreground">{t(($) => $.agent_tasks.bark[feedback])}</p>}
          </form>
        )}
        <DialogFooter>
          <DialogClose render={<Button type="button" variant="ghost" disabled={pending} />}>
            {t(($) => $.agent_tasks.bark.close)}
          </DialogClose>
          {settings.data && (
            <>
              <Button type="button" variant="outline" disabled={!configured || pending} aria-busy={test.isPending} onClick={() => {
                clearFeedback();
                test.mutate();
              }}>
                {test.isPending && <Spinner />}
                {t(($) => $.agent_tasks.bark.test)}
              </Button>
              <Button type="submit" form={`${id}-form`} disabled={pending} aria-busy={save.isPending}>
                {save.isPending && <Spinner />}
                {t(($) => $.agent_tasks.bark.save)}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

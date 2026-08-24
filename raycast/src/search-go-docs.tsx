import { useEffect, useMemo, useState } from "react";
import {
  Action,
  ActionPanel,
  Clipboard,
  Detail,
  Icon,
  Keyboard,
  LaunchProps,
  List,
  Toast,
  getPreferenceValues,
  showToast,
} from "@raycast/api";
import { useExec } from "@raycast/utils";
import { EXEC_ENV, ensureGodocs } from "./godocs";

type Preferences = {
  godocsPath?: string;
  projectDir?: string;
  primaryAction?: "pkgsite" | "detail" | "copy";
  showDetail?: boolean;
};

/** One row of the index TSV emitted by `godocs search`. */
type Entry = {
  id: string;
  kind: string;
  display: string;
  target: string;
  pkg: string;
  anchor: string;
  signature: string;
  synopsis: string;
};

const RESULT_LIMIT = "60";

const KIND_ICON: Record<string, Icon> = {
  pkg: Icon.Box,
  func: Icon.Code,
  type: Icon.Layers,
  method: Icon.ArrowRight,
  const: Icon.Lock,
  var: Icon.Dot,
};

const prefs = getPreferenceValues<Preferences>();

const EXEC_OPTIONS = { cwd: prefs.projectDir || undefined, env: EXEC_ENV };

/** Resolves the godocs binary once, downloading it if this machine lacks one. */
function useGodocsBinary() {
  const [path, setPath] = useState<string | undefined>(undefined);
  const [error, setError] = useState<string | undefined>(undefined);
  const [status, setStatus] = useState<string | undefined>("Looking for godocs");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setError(undefined);
    setStatus("Looking for godocs");

    ensureGodocs(prefs.godocsPath, (message) => {
      if (!cancelled) setStatus(message);
    })
      .then((resolved) => {
        if (cancelled) return;
        setPath(resolved);
        setStatus(undefined);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : String(err));
        setStatus(undefined);
      });

    return () => {
      cancelled = true;
    };
  }, [attempt]);

  return { path, error, status, retry: () => setAttempt((n) => n + 1) };
}

function parseRows(stdout: string): Entry[] {
  const entries: Entry[] = [];
  for (const line of stdout.split("\n")) {
    if (!line) continue;
    const f = line.split("\t");
    if (f.length < 7) continue;
    entries.push({
      id: `${f[0]}:${f[2]}`,
      kind: f[0],
      display: f[1],
      target: f[2],
      pkg: f[3],
      anchor: f[4],
      signature: f[5],
      synopsis: f[6],
    });
  }
  return entries;
}

function pkgsiteURL(entry: Entry): string {
  return `https://pkg.go.dev/${entry.pkg}${entry.anchor ? `#${entry.anchor}` : ""}`;
}

/**
 * Renders what godocs actually said when it failed.
 *
 * A toast would truncate it, and the useful part is usually at the end — the
 * toolchain complaining, say. Showing it in the pane keeps the diagnosis where
 * the problem appeared.
 */
function formatExecError(error: unknown): string {
  const details = error as { stderr?: string; message?: string };
  const stderr = details.stderr?.trim();
  const body = stderr || details.message?.trim() || String(error);
  return [
    "## Could not render documentation",
    "",
    "```",
    body,
    "```",
    "",
    "If this mentions a version manager, godocs picked up a shim that does not",
    "resolve from Raycast's working directory. Clearing its cache makes it",
    "search again:",
    "",
    "```sh",
    "rm ~/.cache/godocs/go-path",
    "```",
  ].join("\n");
}

/** Fetches the rendered Markdown for one entry, for the preview pane. */
function useDocumentation(binary: string | undefined, entry: Entry | undefined) {
  const { data, error, isLoading } = useExec(
    binary ?? "",
    ["render", entry?.pkg ?? "", entry?.anchor ?? "", "--format", "md"],
    {
      ...EXEC_OPTIONS,
      execute: binary !== undefined && entry !== undefined,
      keepPreviousData: false,
      // The error is shown in the pane instead, in full.
      failureToastOptions: { title: "Could not render documentation" },
    },
  );
  return { markdown: error ? formatExecError(error) : data, isLoading };
}

export default function SearchGoDocs(props: LaunchProps<{ arguments: { query?: string } }>) {
  const [searchText, setSearchText] = useState(props.arguments?.query ?? "");
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined);
  const [showDetail, setShowDetail] = useState(prefs.showDetail !== false);
  const godocs = useGodocsBinary();

  const {
    data: entries,
    isLoading: isSearching,
    revalidate,
  } = useExec(godocs.path ?? "", ["search", "--limit", RESULT_LIMIT, searchText], {
    ...EXEC_OPTIONS,
    execute: godocs.path !== undefined,
    keepPreviousData: true,
    parseOutput: ({ stdout }) => parseRows(stdout as string),
    failureToastOptions: { title: "godocs failed" },
  });

  const results = useMemo(() => entries ?? [], [entries]);
  const selected = useMemo(() => results.find((e) => e.id === selectedId) ?? results[0], [results, selectedId]);
  const { markdown, isLoading: isRendering } = useDocumentation(godocs.path, showDetail ? selected : undefined);
  const isLoading = godocs.status !== undefined || isSearching;

  // Nothing works without the binary, so say so plainly instead of showing an
  // empty result list.
  if (godocs.error) {
    return (
      <List searchText={searchText} onSearchTextChange={setSearchText}>
        <List.EmptyView
          icon={Icon.Warning}
          title="godocs is not available"
          description={godocs.error}
          actions={
            <ActionPanel>
              <Action title="Try Again" icon={Icon.ArrowClockwise} onAction={godocs.retry} />
              <Action.CopyToClipboard
                title="Copy Install Command"
                content='GOBIN="$HOME/.local/bin" go install github.com/drewwells/godocs@latest'
              />
              <Action.OpenInBrowser title="Open Godocs on GitHub" url="https://github.com/drewwells/godocs" />
            </ActionPanel>
          }
        />
      </List>
    );
  }

  if (godocs.path === undefined) {
    return (
      <List isLoading searchText={searchText} onSearchTextChange={setSearchText}>
        <List.EmptyView icon={Icon.Download} title={godocs.status ?? "Preparing godocs"} />
      </List>
    );
  }

  // Hoisted so the narrowing survives into the map callback below.
  const binary = godocs.path;

  return (
    <List
      isLoading={isLoading}
      searchText={searchText}
      onSearchTextChange={setSearchText}
      onSelectionChange={(id) => setSelectedId(id ?? undefined)}
      searchBarPlaceholder="Search Go packages, funcs, types, methods…"
      isShowingDetail={showDetail && results.length > 0}
      throttle
    >
      {results.length === 0 && !isLoading ? (
        <List.EmptyView
          icon={Icon.MagnifyingGlass}
          title={searchText ? "No matches" : "Type to search the Go standard library"}
          description={
            searchText ? "Try a shorter query — fuzzy matching works too, e.g. wtgrp for sync.WaitGroup." : undefined
          }
        />
      ) : (
        results.map((entry) => (
          <List.Item
            key={entry.id}
            id={entry.id}
            icon={KIND_ICON[entry.kind] ?? Icon.Circle}
            title={entry.display}
            subtitle={showDetail ? undefined : entry.signature}
            accessories={showDetail ? [{ text: entry.kind }] : [{ text: entry.pkg }]}
            detail={
              <List.Item.Detail
                isLoading={isRendering && entry.id === selected?.id}
                markdown={entry.id === selected?.id ? markdown : undefined}
              />
            }
            actions={
              <Actions
                binary={binary}
                entry={entry}
                showDetail={showDetail}
                onToggleDetail={() => setShowDetail((v) => !v)}
                onReindex={revalidate}
              />
            }
          />
        ))
      )}
    </List>
  );
}

function Actions(props: {
  binary: string;
  entry: Entry;
  showDetail: boolean;
  onToggleDetail: () => void;
  onReindex: () => void;
}) {
  const { binary, entry, showDetail, onToggleDetail, onReindex } = props;

  const openOnPkgsite = <Action.OpenInBrowser key="open" title="Open on Pkg.go.dev" url={pkgsiteURL(entry)} />;
  const showFullDocs = (
    <Action.Push
      key="docs"
      icon={Icon.Book}
      title="Show Full Documentation"
      target={<Documentation binary={binary} entry={entry} />}
    />
  );
  const copyImportPath = (
    <Action.CopyToClipboard key="copy" title="Copy Import Path" content={entry.pkg} icon={Icon.Clipboard} />
  );

  // The preference decides which of the three leads, so Enter does what the
  // user actually wants without reaching for a modifier.
  const ordered = {
    pkgsite: [openOnPkgsite, showFullDocs, copyImportPath],
    detail: [showFullDocs, openOnPkgsite, copyImportPath],
    copy: [copyImportPath, openOnPkgsite, showFullDocs],
  }[prefs.primaryAction ?? "pkgsite"];

  return (
    <ActionPanel>
      <ActionPanel.Section>{ordered}</ActionPanel.Section>
      <ActionPanel.Section>
        <Action.CopyToClipboard
          title="Copy Symbol"
          content={entry.display}
          icon={Icon.Text}
          shortcut={Keyboard.Shortcut.Common.Copy}
        />
        <Action.CopyToClipboard
          title="Copy Go Doc Command"
          content={`go doc ${entry.target}`}
          icon={Icon.Terminal}
          shortcut={{ modifiers: ["cmd", "shift"], key: "g" }}
        />
        <Action.CopyToClipboard
          title="Copy Import Statement"
          content={`import "${entry.pkg}"`}
          icon={Icon.Code}
          shortcut={{ modifiers: ["cmd", "shift"], key: "i" }}
        />
      </ActionPanel.Section>
      <ActionPanel.Section>
        <Action
          title={showDetail ? "Hide Preview" : "Show Preview"}
          icon={Icon.Sidebar}
          onAction={onToggleDetail}
          shortcut={{ modifiers: ["cmd"], key: "d" }}
        />
        <Action
          title="Rebuild Index"
          icon={Icon.ArrowClockwise}
          shortcut={Keyboard.Shortcut.Common.Refresh}
          onAction={async () => {
            const toast = await showToast({ style: Toast.Style.Animated, title: "Rebuilding index…" });
            try {
              await rebuildIndex(binary);
              toast.style = Toast.Style.Success;
              toast.title = "Index rebuilt";
              onReindex();
            } catch (error) {
              toast.style = Toast.Style.Failure;
              toast.title = "Rebuild failed";
              toast.message = error instanceof Error ? error.message : String(error);
            }
          }}
        />
      </ActionPanel.Section>
    </ActionPanel>
  );
}

/** Full-window documentation, for symbols whose docs outgrow the preview pane. */
function Documentation({ binary, entry }: { binary: string; entry: Entry }) {
  const { markdown, isLoading } = useDocumentation(binary, entry);
  return (
    <Detail
      isLoading={isLoading}
      markdown={markdown}
      navigationTitle={entry.display}
      actions={
        <ActionPanel>
          <Action.OpenInBrowser title="Open on Pkg.go.dev" url={pkgsiteURL(entry)} />
          <Action.CopyToClipboard title="Copy Import Path" content={entry.pkg} />
          <Action title="Copy Documentation" icon={Icon.Clipboard} onAction={() => Clipboard.copy(markdown ?? "")} />
        </ActionPanel>
      }
    />
  );
}

async function rebuildIndex(binary: string): Promise<void> {
  const { execFile } = await import("child_process");
  const { promisify } = await import("util");
  await promisify(execFile)(binary, ["index", "--force"], EXEC_OPTIONS);
}

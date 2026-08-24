import { useMemo, useState } from "react";
import { accessSync, constants } from "fs";
import { homedir } from "os";
import { join } from "path";
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

function expandHome(p: string): string {
  return p.startsWith("~/") ? join(homedir(), p.slice(2)) : p;
}

// Raycast starts processes with a minimal PATH, so put the usual install
// locations on it before looking for anything.
const SEARCH_PATH = [
  join(homedir(), ".local", "bin"),
  join(homedir(), "go", "bin"),
  "/opt/homebrew/bin",
  "/usr/local/bin",
  join(homedir(), ".local", "share", "mise", "shims"),
  process.env.PATH ?? "/usr/bin:/bin",
].join(":");

const EXEC_ENV = { ...process.env, PATH: SEARCH_PATH };

/** Locates the godocs binary: an explicit preference, then PATH. */
function findGodocs(): string {
  const configured = prefs.godocsPath?.trim();
  if (configured) return expandHome(configured);

  for (const dir of SEARCH_PATH.split(":")) {
    const candidate = join(dir, "godocs");
    try {
      accessSync(candidate, constants.X_OK);
      return candidate;
    } catch {
      // Not here; keep looking.
    }
  }
  // Fall back to the bare name so the failure toast names the real problem.
  return "godocs";
}

const GODOCS = findGodocs();

const EXEC_OPTIONS = { cwd: prefs.projectDir || undefined, env: EXEC_ENV };

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

/** Fetches the rendered Markdown for one entry, for the preview pane. */
function useDocumentation(entry: Entry | undefined) {
  const { data, isLoading } = useExec(GODOCS, ["render", entry?.pkg ?? "", entry?.anchor ?? "", "--format", "md"], {
    ...EXEC_OPTIONS,
    execute: entry !== undefined,
    keepPreviousData: false,
    failureToastOptions: { title: "Could not render documentation" },
  });
  return { markdown: data, isLoading };
}

export default function SearchGoDocs(props: LaunchProps<{ arguments: { query?: string } }>) {
  const [searchText, setSearchText] = useState(props.arguments?.query ?? "");
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined);
  const [showDetail, setShowDetail] = useState(prefs.showDetail !== false);

  const {
    data: entries,
    isLoading,
    revalidate,
  } = useExec(GODOCS, ["search", "--limit", RESULT_LIMIT, searchText], {
    ...EXEC_OPTIONS,
    keepPreviousData: true,
    parseOutput: ({ stdout }) => parseRows(stdout as string),
    failureToastOptions: {
      title: "godocs failed",
      message:
        "Install it with: GOBIN=$HOME/.local/bin go install github.com/drewwells/godocs@latest",
    },
  });

  const results = useMemo(() => entries ?? [], [entries]);
  const selected = useMemo(() => results.find((e) => e.id === selectedId) ?? results[0], [results, selectedId]);
  const { markdown, isLoading: isRendering } = useDocumentation(showDetail ? selected : undefined);

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

function Actions(props: { entry: Entry; showDetail: boolean; onToggleDetail: () => void; onReindex: () => void }) {
  const { entry, showDetail, onToggleDetail, onReindex } = props;

  const openOnPkgsite = <Action.OpenInBrowser key="open" title="Open on Pkg.go.dev" url={pkgsiteURL(entry)} />;
  const showFullDocs = (
    <Action.Push key="docs" icon={Icon.Book} title="Show Full Documentation" target={<Documentation entry={entry} />} />
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
              await rebuildIndex();
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
function Documentation({ entry }: { entry: Entry }) {
  const { markdown, isLoading } = useDocumentation(entry);
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

async function rebuildIndex(): Promise<void> {
  const { execFile } = await import("child_process");
  const { promisify } = await import("util");
  await promisify(execFile)(GODOCS, ["index", "--force"], EXEC_OPTIONS);
}

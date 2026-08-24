/**
 * Locating the godocs binary.
 *
 * In order of preference:
 *   1. an explicit path from preferences
 *   2. godocs already on PATH — `go install` puts it there, and anyone with a
 *      Go toolchain probably took that route
 *   3. a copy this extension downloads from the pinned GitHub release,
 *      verified against a checksum recorded in source
 *
 * Step 3 is what lets the extension work with no setup. It deliberately does
 * not build anything on the user's machine, and it never trusts a checksum
 * served from the same place as the download.
 */
import { createHash } from "crypto";
import { accessSync, constants as fsConstants } from "fs";
import { chmod, mkdir, stat, rename, writeFile } from "fs/promises";
import { homedir } from "os";
import { join } from "path";
import { gunzip } from "zlib";
import { promisify } from "util";
import { environment } from "@raycast/api";
import { RELEASE, assetURL, expectedChecksum, isPinned } from "./release";

export { RELEASE } from "./release";

const gunzipAsync = promisify(gunzip);

/** Raycast starts processes with a minimal PATH; these are the usual homes. */
export const SEARCH_PATH = [
  join(homedir(), ".local", "bin"),
  join(homedir(), "go", "bin"),
  "/opt/homebrew/bin",
  "/usr/local/bin",
  join(homedir(), ".local", "share", "mise", "shims"),
  process.env.PATH ?? "/usr/bin:/bin",
].join(":");

export const EXEC_ENV = { ...process.env, PATH: SEARCH_PATH };

export function expandHome(p: string): string {
  return p.startsWith("~/") ? join(homedir(), p.slice(2)) : p;
}

function isExecutable(path: string): boolean {
  try {
    accessSync(path, fsConstants.X_OK);
    return true;
  } catch {
    return false;
  }
}

export function findOnPath(): string | undefined {
  for (const dir of SEARCH_PATH.split(":")) {
    if (!dir) continue;
    const candidate = join(dir, "godocs");
    if (isExecutable(candidate)) return candidate;
  }
  return undefined;
}

/**
 * Where a downloaded binary lives. The version is in the filename so that
 * bumping the pinned release fetches afresh rather than reusing a stale copy.
 */
export function managedPath(): string {
  return join(environment.supportPath, "bin", `godocs-${RELEASE.version}`);
}

export class GodocsUnavailable extends Error {
  constructor(message: string) {
    super(message);
    this.name = "GodocsUnavailable";
  }
}

const INSTALL_HINT = 'Install it with:\n\nGOBIN="$HOME/.local/bin" go install github.com/drewwells/godocs@latest';

/**
 * Returns a usable godocs binary, downloading it if necessary.
 *
 * onProgress reports the one slow step so the UI can say what it is waiting on.
 */
export async function ensureGodocs(
  configuredPath: string | undefined,
  onProgress?: (message: string) => void,
): Promise<string> {
  const configured = configuredPath?.trim();
  if (configured) {
    const path = expandHome(configured);
    if (isExecutable(path)) return path;
    throw new GodocsUnavailable(`No executable at ${path}. Clear the godocs Path preference to search PATH instead.`);
  }

  const onPath = findOnPath();
  if (onPath) return onPath;

  const managed = managedPath();
  if (isExecutable(managed)) return managed;

  if (!isPinned()) {
    throw new GodocsUnavailable(
      `godocs was not found on PATH, and this build has no pinned release to download.\n\n${INSTALL_HINT}`,
    );
  }

  const url = assetURL();
  const checksum = expectedChecksum();
  if (!url || !checksum) {
    throw new GodocsUnavailable(`No godocs build for ${process.arch} macOS.\n\n${INSTALL_HINT}`);
  }

  onProgress?.(`Downloading godocs ${RELEASE.version}`);
  await download(url, checksum, managed);
  return managed;
}

async function download(url: string, expected: string, destination: string): Promise<void> {
  let response: Response;
  try {
    response = await fetch(url, { redirect: "follow" });
  } catch (error) {
    throw new GodocsUnavailable(`Could not reach GitHub to download godocs: ${describe(error)}\n\n${INSTALL_HINT}`);
  }
  if (!response.ok) {
    throw new GodocsUnavailable(`Download failed: ${response.status} ${response.statusText}\n\n${INSTALL_HINT}`);
  }

  const payload = Buffer.from(await response.arrayBuffer());

  // Verify before decompressing: never hand unverified bytes to a decoder, and
  // never write them anywhere executable.
  const actual = createHash("sha256").update(payload).digest("hex");
  if (actual !== expected) {
    throw new GodocsUnavailable(
      `Checksum mismatch for ${url}.\nExpected ${expected}\nGot      ${actual}\n\nThe download was discarded.`,
    );
  }

  const binary = await gunzipAsync(payload);

  await mkdir(join(destination, ".."), { recursive: true });
  // Write to a temporary name and rename, so a partial write can never be
  // mistaken for a usable binary.
  const temporary = `${destination}.partial`;
  await writeFile(temporary, binary, { mode: 0o755 });
  await chmod(temporary, 0o755);
  await rename(temporary, destination);
}

export async function managedBinaryInfo(): Promise<{ path: string; bytes: number } | undefined> {
  const path = managedPath();
  try {
    const info = await stat(path);
    return { path, bytes: info.size };
  } catch {
    return undefined;
  }
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

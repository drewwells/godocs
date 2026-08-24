#!/usr/bin/env node
/**
 * Pins src/release.ts to a published godocs release.
 *
 * Usage:
 *   npm run sync-release -- v1.2.3
 *   npm run sync-release -- v1.2.3 --from ../dist/checksums.txt
 *
 * Without --from, the checksums come from the release's own checksums.txt on
 * GitHub. That is fine here — this runs at authoring time, and the result is
 * committed to source where a reviewer can see it. What must never happen is
 * the *extension* trusting a checksum fetched at runtime.
 */
import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const releaseFile = join(here, "..", "src", "release.ts");

const args = process.argv.slice(2);
const version = args.find((a) => !a.startsWith("--"));
const fromIndex = args.indexOf("--from");
const from = fromIndex >= 0 ? args[fromIndex + 1] : undefined;

if (!version) {
  console.error("usage: npm run sync-release -- <tag> [--from <checksums.txt>]");
  process.exit(2);
}

const source = await readFile(releaseFile, "utf8");
const repo = source.match(/repo:\s*"([^"]+)"/)?.[1];
if (!repo) {
  console.error(`could not read repo from ${releaseFile}`);
  process.exit(1);
}

let checksums;
if (from) {
  checksums = await readFile(from, "utf8");
} else {
  const url = `https://github.com/${repo}/releases/download/${version}/checksums.txt`;
  const response = await fetch(url, { redirect: "follow" });
  if (!response.ok) {
    console.error(`fetching ${url}: ${response.status} ${response.statusText}`);
    process.exit(1);
  }
  checksums = await response.text();
}

// sha256sum output: "<hex>  <path>"
const wanted = {
  arm64: `godocs_${version}_darwin_arm64.gz`,
  x64: `godocs_${version}_darwin_amd64.gz`,
};

const found = {};
for (const line of checksums.split("\n")) {
  const match = line.trim().match(/^([0-9a-f]{64})\s+\.?\/?(.+)$/);
  if (!match) continue;
  for (const [arch, name] of Object.entries(wanted)) {
    if (match[2] === name) found[arch] = match[1];
  }
}

const missing = Object.keys(wanted).filter((arch) => !found[arch]);
if (missing.length) {
  console.error(`no checksum for: ${missing.map((a) => wanted[a]).join(", ")}`);
  process.exit(1);
}

const updated = source
  .replace(/version:\s*"[^"]*"/, `version: "${version}"`)
  .replace(/arm64:\s*"[^"]*"/, `arm64: "${found.arm64}"`)
  .replace(/x64:\s*"[^"]*"/, `x64: "${found.x64}"`);

await writeFile(releaseFile, updated);
console.log(`pinned ${repo} ${version}`);
for (const [arch, hash] of Object.entries(found)) {
  console.log(`  ${arch.padEnd(6)} ${hash}`);
}

#!/usr/bin/env node
/**
 * Exercises the real download-and-verify path in src/godocs.ts against locally
 * built release artifacts.
 *
 * The module is bundled with esbuild and given a stub @raycast/api, then fetch
 * is replaced with one that serves files from a directory. Nothing in the
 * production code is modified for the test, so what runs here is what ships.
 *
 * Usage: node scripts/test-download.mjs <dist-dir> <version>
 */
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import * as esbuild from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
const [distDir, version] = process.argv.slice(2);
if (!distDir || !version) {
  console.error("usage: node scripts/test-download.mjs <dist-dir> <version>");
  process.exit(2);
}

// godocs.ts searches PATH and ~/.local/bin first, and would find this machine's
// real installation instead of testing the download. Re-run under a scrubbed
// environment so the managed path is the only option.
if (!process.env.GODOCS_TEST_SCRUBBED) {
  const fakeHome = await mkdtemp(join(tmpdir(), "godocs-home-"));
  const result = spawnSync(process.execPath, [fileURLToPath(import.meta.url), distDir, version], {
    stdio: "inherit",
    env: {
      ...process.env,
      HOME: fakeHome,
      PATH: "/usr/bin:/bin",
      GODOCS_TEST_SCRUBBED: "1",
    },
  });
  await rm(fakeHome, { recursive: true, force: true });
  process.exit(result.status ?? 1);
}

let failures = 0;
function check(name, ok, detail = "") {
  console.log(`${ok ? "  ok  " : "FAIL  "}${name}${detail ? ` — ${detail}` : ""}`);
  if (!ok) failures++;
}

const support = await mkdtemp(join(tmpdir(), "godocs-test-"));

// Stub the two @raycast/api members godocs.ts touches.
const stubPlugin = {
  name: "stub-raycast",
  setup(build) {
    build.onResolve({ filter: /^@raycast\/api$/ }, () => ({ path: "raycast-stub", namespace: "stub" }));
    build.onLoad({ filter: /.*/, namespace: "stub" }, () => ({
      contents: `export const environment = { supportPath: ${JSON.stringify(support)}, isDevelopment: true };`,
      loader: "js",
    }));
  },
};

const bundle = join(support, "godocs.mjs");
await esbuild.build({
  entryPoints: [join(here, "..", "src", "godocs.ts")],
  outfile: bundle,
  bundle: true,
  format: "esm",
  platform: "node",
  target: "node22",
  plugins: [stubPlugin],
  logLevel: "silent",
});

const mod = await import(pathToFileURL(bundle).href);

// Pin the bundled RELEASE to the locally built artifacts.
const checksums = await readFile(join(distDir, "checksums.txt"), "utf8");
const hashFor = (name) => checksums.split("\n").find((l) => l.includes(name))?.trim().split(/\s+/)[0];

const arch = process.arch === "arm64" ? "arm64" : "amd64";
const asset = `godocs_${version}_darwin_${arch}.gz`;
const expected = hashFor(asset);
if (!expected) {
  console.error(`no checksum for ${asset} in ${distDir}/checksums.txt`);
  process.exit(1);
}

// Serve the artifacts without a network.
const served = new Map();
globalThis.fetch = async (url) => {
  const name = String(url).split("/").pop();
  if (served.has(name)) {
    return { ok: true, status: 200, statusText: "OK", arrayBuffer: async () => served.get(name) };
  }
  try {
    const data = await readFile(join(distDir, name));
    return { ok: true, status: 200, statusText: "OK", arrayBuffer: async () => data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength) };
  } catch {
    return { ok: false, status: 404, statusText: "Not Found", arrayBuffer: async () => new ArrayBuffer(0) };
  }
};

console.log(`\ntesting ${asset}`);

// --- 1. a good download installs a working binary --------------------------
const rel = mod.RELEASE;
if (!rel) {
  console.error("expected the godocs.ts bundle to re-export RELEASE");
  process.exit(1);
}
rel.version = version;
rel.checksums.arm64 = hashFor(`godocs_${version}_darwin_arm64.gz`) ?? "";
rel.checksums.x64 = hashFor(`godocs_${version}_darwin_amd64.gz`) ?? "";

const installed = await mod.ensureGodocs(undefined, (m) => console.log(`  … ${m}`));
check("downloads and installs", installed.startsWith(support), installed);

const info = await stat(installed);
check("is executable", (info.mode & 0o111) !== 0, `mode ${(info.mode & 0o777).toString(8)}`);

// A darwin binary only runs here if this machine is darwin and the arch matches.
if (process.platform === "darwin") {
  try {
    const out = execFileSync(installed, ["--help"], { encoding: "utf8" });
    check("the installed binary runs", out.includes("godocs - fast fuzzy lookup"));
  } catch (error) {
    check("the installed binary runs", false, error.message);
  }
}

// --- 2. a tampered download is rejected ------------------------------------
await rm(installed, { force: true });
const good = await readFile(join(distDir, asset));
const tampered = Buffer.from(good);
tampered[tampered.length - 1] ^= 0xff;
served.set(asset, tampered.buffer.slice(tampered.byteOffset, tampered.byteOffset + tampered.byteLength));

let rejected = false;
let message = "";
try {
  await mod.ensureGodocs(undefined);
} catch (error) {
  rejected = true;
  message = error.message;
}
check("rejects a tampered payload", rejected && /Checksum mismatch/.test(message));

let wroteAnyway = true;
try {
  await stat(mod.managedPath());
} catch {
  wroteAnyway = false;
}
check("writes nothing on mismatch", !wroteAnyway);

// --- 3. an explicit preference wins ----------------------------------------
const fake = join(support, "custom-godocs");
await writeFile(fake, "#!/bin/sh\nexit 0\n", { mode: 0o755 });
const chosen = await mod.ensureGodocs(fake);
check("honours the godocs Path preference", chosen === fake);

await rm(support, { recursive: true, force: true });
console.log(failures ? `\n${failures} failed` : "\nall passed");
process.exit(failures ? 1 : 0);

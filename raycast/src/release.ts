/**
 * The godocs release this extension is pinned to.
 *
 * The checksums are recorded here, in reviewable source, rather than fetched
 * alongside the binary — a checksum file served from the same place as the
 * download would prove nothing. Pinning also means a reviewer can see exactly
 * which bytes the extension will fetch.
 *
 * Regenerate with `npm run sync-release -- <tag>` after publishing a release.
 */
export const RELEASE = {
  repo: "drewwells/godocs",
  version: "v0.1.0",
  /** SHA-256 of each gzipped binary, keyed by Node's process.arch. */
  checksums: {
    arm64: "756b07f7cc7a5790cf29111368bdc1485d318335b2dd53cca1af2cf515129e22",
    x64: "b3b74a9cb72460d4ef27ec4a6b9e1742ffee019a58e29916f71f52d4014b5236",
  } as Record<string, string>,
};

/** Maps Node's architecture names onto Go's. */
const GOARCH: Record<string, string> = {
  arm64: "arm64",
  x64: "amd64",
};

export function assetName(arch: string = process.arch): string | undefined {
  const goarch = GOARCH[arch];
  if (!goarch) return undefined;
  return `godocs_${RELEASE.version}_darwin_${goarch}.gz`;
}

export function assetURL(arch: string = process.arch): string | undefined {
  const name = assetName(arch);
  if (!name) return undefined;
  return `https://github.com/${RELEASE.repo}/releases/download/${RELEASE.version}/${name}`;
}

export function expectedChecksum(arch: string = process.arch): string | undefined {
  return RELEASE.checksums[arch] || undefined;
}

export function isPinned(): boolean {
  return RELEASE.version !== "v0.0.0" && Boolean(expectedChecksum());
}

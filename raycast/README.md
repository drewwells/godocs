# Go Docs

Offline fuzzy search over the Go standard library — and, optionally, a
project's dependencies — with the documentation rendered inline.

This is a front end for the `godocs` CLI in the repository root; the extension
shells out to it for both search and rendering, so Raycast and the terminal
picker always return the same results in the same order.

## Setup

```sh
GOBIN="$HOME/.local/bin" go install github.com/drewwells/godocs@latest
npm install
npx ray develop      # registers the extension with Raycast
```

Set an alias of `go` on the **Search Go Docs** command in Raycast's extension
preferences to search with `go {term}`.

## Preferences

| Preference | Default | Notes |
|---|---|---|
| godocs Path | — | Leave blank to find `godocs` on PATH. |
| Project Directory | — | A Go module whose dependencies should be searched too. Run `godocs deps` in it once first. |
| Enter Key | Open on pkg.go.dev | Or show full documentation, or copy the import path. |
| Preview | on | Render docs beside the results. |

## Notes

`author` in `package.json` is not a registered Raycast username, so `ray lint`
reports it. That check only matters for publishing to the Raycast store; local
development and `ray build` are unaffected.

# Repository plugin builds

The user approved one managed entry per repository, with SourceEgg downloading
and compiling SourcePawn directly instead of requiring GitLab CI releases. The
existing preview, exact-package approval, next-normal-restart installation,
configuration preservation, uninstall and two-version rollback behavior remain.

## Approach and boundaries

Retain the current release-asset mode. An optional `build` object selects source
mode. Resolve a branch/tag/commit through the provider API, download an archive
at the resolved full commit, and compile in private staging. This provides the
requested repository workflow without requiring Git, checkout credentials in
process arguments, or repository-controlled scripts. A central compiled bundle
and per-repository CI releases were considered; the user chose independent
repository builds here.

Support the existing GitLab and public GitHub providers. Reuse named GitLab
credentials and same-origin HTTP authentication. Recommend `read_api` for the
GitLab API calls. Source mode does not execute git hooks, shell scripts,
Makefiles or native-extension builds, fetch submodules, or solve dependencies.
SourcePawn plugins with explicit include repositories are the scope.

## Recipe

`source.ref` selects the branch/tag/full commit; omitted means `main` for source
builds. Release recipes cannot set it. Source builds omit `asset`, `format`,
`pin`, and `prerelease`; use `source.ref` for an exact tag or commit.

`build.sourcemod` is a required exact upstream SourceMod release tag. Fetch the
Linux archive using the existing trusted framework cache under the server's
private `.source/plugins/toolchains`. Use its `scripting/spcomp64` when present,
otherwise `scripting/spcomp`, and its standard includes. Toolchain failures do
not fall back to another version or an executable from the source repository.

`build.sources` contains 1–100 explicit repository-relative `.sp` paths or glob
patterns. Expand in sorted order, reject missing patterns and duplicate output
basenames, and compile to `compiled/<source-basename>.smx`. A repository may not
already contain the reserved `compiled` path. `build.include_dirs` names up to
100 repository-relative include directories; `.` is allowed. Each compiled file
also searches its own source directory and the trusted compiler includes.

`build.dependencies` optionally contains up to 20 objects with a `source` and
1–100 `include_dirs`. Each dependency is resolved to an exact commit and used
only for includes. Credentials use the same named-credential store. Changes to
any dependency commit produce a new available build even when the main plugin
commit is unchanged. There are no transitive or automatically installed runtime
dependencies.

Existing `mappings` apply to the source repository root after compilation. Map
`compiled` to `addons/sourcemod/plugins`, or an individual output to a renamed or
disabled plugin path. Map gamedata, translations and config from their original
repository-relative locations. Existing allowed-destination and ownership rules
continue to apply, including stock SourceMod file ownership.

## Build and identity

Source versions identify the primary full commit and a digest of the resolved
include dependency revisions and exact compiler tag. Recipe hashing includes
every build option. Staging produces normal immutable package snapshots; the
approval binds actual output bytes and the complete recipe. Do not rebuild an
unchanged available snapshot unnecessarily. A later commit cannot change a
previously approved pending install.

Archives retain existing 256 MiB download, 512 MiB expansion, 64 MiB per-file,
10,000-entry, path traversal and link rejection limits. Remove exactly one
provider-generated archive wrapper directory; reject ambiguous archive roots.
Generated output must be a regular nonempty .smx file within the same size limit
and have the SourcePawn binary magic. Validate copied outputs through the same
package-mapping path used by release packages.

Run only the trusted compiler with fixed argument construction (no shell), a
minimal environment without provider credentials, bounded diagnostic capture,
and cancellation/time limits. Source work is cleaned after success or failure.
This is staging, not an OS security sandbox; only compile source you trust, and
do not claim protection from native compiler vulnerabilities or filesystem
reads by the compiler. Return useful bounded diagnostics with named credential
values redacted.

Use a bounded per-plugin build/check timeout of five minutes, and a shorter
per-compilation timeout within it. Preserve normal release checks. A failed
fetch, dependency resolution, toolchain acquisition or compile retains installed
files and any earlier compatible staged snapshot. No hot reloads, forced
restarts, automatic approvals, framework replacement or credential publication.

## Verification

Use HTTP fixtures for commit/ref lookup, immutable archive URLs, dependency
revision identity, token origin confinement and malformed metadata. Use a small
test compiler process for end-to-end command construction, output validation,
failure/cancellation and secret-free environment. Add a real compiler smoke
check when an upstream toolchain is reachable. Exercise source builds through
the manager's approval/restart/update/rollback workflow and run existing release
and framework tests unchanged.

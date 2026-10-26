# Upstream fork parity

The user approved the October 26 comparison and requested full applicable upstream behavior, with cron password scheduling as a deliberate extension. This is authorization to implement the comparison, not another proposal round. Preserve the five Source 1 games and existing private/public storage boundaries. Use the existing Source 1 logo with the supplied EdgeGamers mark, as explicitly selected by the user.

## Approach

Port applicable S2Script CS2 Egg interfaces and behavior into the existing Go implementation, using upstream commit 8857a5519ac4cb56b26716f6d57f7956f60d3a8e as the reference. A wholesale source replacement would discard the five-game architecture and existing immutable generations; a cosmetic-only port would leave the approved behavior gaps open. The selected approach preserves those foundations while making the operator experience act like an upstream fork.

## Requirements

- Match upstream terminal presentation: section dividers, help adjacent to each question, described choices, module overview, settings review before writing, installation progress, and contextual command reference. Render both logos as embedded terminal text, without runtime image dependencies. Respect NO_COLOR and plain/nonterminal output; keep narrow terminals readable. Never reveal saved passwords or webhook tokens.
- Expose framework refresh, retention, and fleet pins; backup filters and independent unlimited retention; cleanup frequency/on-start/open-file and per-rule retention; maintenance schedules/tasks/journal retention/disk threshold; alert event/identity controls; metrics sample interval; channel selection including dev; password master enable, custom dictionary/state path, named interval choices and cron/timezone. Preserve saved choices and custom rules on reruns.
- Cron uses standard five fields (minute, hour, day-of-month, month, weekday), ranges/lists/steps and IANA timezone, with explicit validation and DST behavior. Legacy hours/calendar months keep working. Saved assignments retain their due dates until ordinary rotation; no repeated catch-up rotations. Disabling generation must preserve cached assignments. Private credential delivery stays separate.
- Match upstream game delivery choices (read-only links, hardlinks, copies), concurrency controls, optional automatic game-update restarts, node Steam validation, and empty-or-window restart semantics. Keep immutable generations, game isolation, and symlink/traversal protections.
- Add server-side cleanup fallback with editable rules, console filters/log levels, explicit startup parameters, opt-in remote debugging, and stock SourceMod plugin toggles corresponding to upstream's plugin controls. Engine-specific CS2 dependencies/guard/workshop remain excluded as documented in the approved comparison.
- Render operator status/inventories as readable tables/sections with a JSON escape hatch. Keep current commands compatible. Align fresh-install choices/defaults where applicable, but do not silently turn on new maintenance/backups/cleanup/password features for existing installations.
- Update attribution and configuration/operations docs, and replace obsolete claims that parity is unmerged. Prove changes with fixture-based behavior tests and full Go race/vet/build checks. Live game acceptance and image smoke are reported separately if host facilities are unavailable.

## Decisions delegated to implementation

Retain Source-specific names, per-game password scope, private credential webhook, and immutable-generation rollback. Reuse rooted filesystem helpers. New config keys must load old files without data loss. Never install/restart a live node, prune the host, publish releases, or deploy while testing. Default cron timezone is UTC unless an operator explicitly chooses another zone. Display the next scheduled run before saving.

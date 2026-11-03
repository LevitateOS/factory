# Licensing and retained source-review notes

Original SodaOS code/documentation/configuration are covered by the root
[Apache-2.0 LICENSE](../../LICENSE), with scope and attribution in [NOTICE](../../NOTICE)
and the README. This grant does not relicense inherited code, canonical artwork
or third-party material. Preserve their existing terms and provenance.

These notes preserve useful findings from the removed Forgejo source-build guide.
They are not a build recipe, dependency lock, distribution approval or complete
license-clearance claim. The selected integration is Fountain, a maintained
Forgejo 15.0 LTS fork compiled from an exact committed source input. The current
upstream update procedure belongs to the
[release workflow](../development/release.md#fountain-upstream-maintenance).

## Distribution obligations

Original Soda robot v1 artwork is Apache-2.0, with provenance in
[`internal/avatar/README.md`](../../internal/avatar/README.md). The renderer uses no
Bottts artwork. Exact DiceBear/core-schema/validator/text dependency license texts
are retained in [`avatar-dependencies.txt`](../../system/licenses/avatar-dependencies.txt)
and copied into new native bundles; this is not broader distribution clearance.

- Forgejo's overall GPL-3.0-or-later and its per-file/template terms remain applicable
  to Fountain. Swagger's MIT grant is not the Forgejo distribution license. Building
  a patched executable on an official image does not waive corresponding-source or
  notice duties.
- Preserve copyright/license notices in overridden upstream templates and assets,
  document modifications as required and deliver their applicable source/notices.
  Do not stamp inherited templates as original Apache-only Soda code.
- The actual candidate retains the exact Fountain Git archive used for compilation.
  That archive contains the root GPL license, nested SDK source/manifests,
  extension authoring documentation/example, and the license plus modification
  note for the vendored session dependency. The separate SodaOS source archive
  contains the Soda extension package/backend sources; bundle notices carry the
  SodaOS Apache license and notice. Include required source and licenses for other
  distributed dependencies/runtime material; a tag URL or generic license-name
  list is not automatically sufficient. Exclude private configuration, credentials,
  databases, repositories and evidence from distributable payloads.
- Retain MIT/BSD notices, Apache NOTICE, applicable MPL covered source, LGPL
  source/relinking requirements and OFL/CC attribution according to actual shipped
  content. Review embedded assets/linkage, not only container labels or `dev` flags.
- The inspected predecessor checkout had no root LICENSE/COPYING grant. Preserve
  file-level/vendor rights and [provenance](predecessor-reuse.md); do not infer that
  inherited code/artwork is Apache-licensed merely because it resides here.
  Unresolved rights/source obligations stop distribution; they are not fixed by
  deleting a notice or assuming permission. The predecessor remains unchanged.

Spaces uses two unmodified Primer Octicons 19.14.0 SVGs, matching the icon package
selected by Forgejo 15.0 LTS. Their [source and MIT notice](../../assets/branding/icons/octicons/README.md)
are staged with the icons; this does not add a JS package dependency or change
Forgejo's own licensing obligations.

## Retained concrete findings

From the U01 review at `542de21`, whose architecture acceptance was later withdrawn:

- Apache license text was retrieved verbatim from the Apache Software Foundation.
- PatternFly 6.6.1's actual `LICENSE.txt` at published npm Git commit
  `26b709bfeb14c3643a6b999a2619b6bd65641ffa` grants MIT, copyright 2019 Red Hat, Inc.
  All 33 installed font files matched that source's Git blob IDs. Those include
  Red Hat Display/Text/Mono and Font Awesome Solid.
- Retrieved Red Hat Font OFL/author texts as verified Git blobs, including Reserved
  Font Name Red Hat. Font Awesome 5.0.13 distinguishes fonts (OFL-1.1), SVG/JS icons
  (CC-BY-4.0) and other code (MIT). Actual shipped-font/notice pairing remains a
  packaging check; an arbitrary current OFL text is not proof for every version.
- The historical `cockpit/build/licenses.ts` exempted `@patternfly/*` from missing
  license-text refusal; the removed dashboard also used it. The custom Cockpit build
  is now retired, not a complete compliance finding for those historical bundles.
  Stock branding stages the current canonical font licenses independently; retained
  vendor attribution is preserved in
  [`assets/branding/cockpit/provenance/`](../../assets/branding/cockpit/provenance/README.md)
  after removing the retired top-level workspace.
- Top-level license/NOTICE texts for all 13 modules named in Soda's go.mod were
  reviewed from exact-version caches: MIT/BSD and YAML's per-file MIT/Apache split
  plus NOTICE. This is not the full dependency graph or final binary closure.
- The researched Forgejo v16.0.3 npm lock had 1,220 entries. Three missing license
  declarations (`khroma` 2.1.0, `reserved` 0.1.2, `svg-tags` 1.0.0) were resolved
  from exact integrity-verified tarballs as MIT. That research does not select v16
  for deployment or certify the shipped stock image's closure.
- The researched upstream Go-license target tolerated collection errors, and its
  generator omitted NOTICE/README. Its 260 generated notices lacked version/build-
  tag binding. Do not present that metadata as complete actual-artifact compliance.
- Retain Cockpit LGPL and Tea license texts, runner payload notices and canonical
  branding attribution. HTMX and its matching license are removed from current
  source together; historical bundles/evidence still need their original notices.

The full Forgejo presentation inventory includes the selected 15.0 LTS GPL text
(`system/licenses/forgejo-LICENSE`), Soda LICENSE/NOTICE, all three bundled font
family licenses, and the locked source metadata for the complete merged English
catalog. Fountain's exact source input is retained separately by the candidate
producer because the presentation inventory is not corresponding source. Neither
record is final shipped-image license closure.

Research evidence remains in `.artifacts/research/u01-d5b5065/` and
`.artifacts/research/u01-8727233/`, with provenance, license texts, hash comparisons
and retained failures. Earlier source-preparation evidence is historical only.
The [handoff](../development/README.md) records exactly what ran. No notices have
been newly packaged or installed by moving these notes.

## Lit runtime

The Lit scaffold stages the exact BSD-3-Clause text from its resolved npm package
as `public/assets/soda/forgejo/lit.LICENSE`, retained in
[`system/licenses/lit-LICENSE`](../../system/licenses/lit-LICENSE). The bundled
core (`lit`, `lit-element`, `lit-html`, `@lit/reactive-element`) shares that license
text and copyright; dependency identities remain in the root Bun lockfile. This
adds the browser runtime's notice, not a wider distribution-clearance claim.

## Terminal transport dependency

The native terminal source adds `github.com/coder/websocket`, pinned with real module
checksums in `go.mod`/`go.sum`. Its v1.8.15 `LICENSE.txt` is ISC, not MIT; the verbatim
copyright/permission/warranty text is included in root `NOTICE`, which the existing
bundle already carries. Xterm 6.0.0 and fit 0.11.0 are now pinned in
`tools/release-assets/terminal-assets.lock.json` with real archive/file integrity. The native
build fetches their exact ES-module/CSS distributions and stages both verbatim MIT
notices beside them; no CDN or runtime download is used. This records dependency
notices, not complete binary/runtime license closure or deployment.

## Upgrade research is not a deployment selection

Fountain currently derives from Forgejo 15.0.10. The prior v15/v16 comparison and
release-note review remain historical evidence, not selection of another release.
Assess maintenance and security releases on the maintained 15.0 LTS line and update
the source and runtime base together through the release workflow. A major upgrade
requires its own compatibility and migration decision. Only Forgejo performs its
migrations; selecting an older image is not data rollback.

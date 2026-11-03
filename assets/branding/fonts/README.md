# Soda brand fonts

Self-hosted WOFF2 assets matching the Soda OS website's `src/app/styles/fonts.css`
and Fontsource package versions (5.3.0), downloaded from the npm registry.

| Family | Normal weights | Use |
| --- | --- | --- |
| Barlow Condensed | 800 | Forgejo display headings |
| Fraunces Variable | 100–900 | Retained for existing consumers outside Forgejo |
| Barlow | 400, 600 | Body text and interface |
| IBM Plex Mono | 400, 500 | Code and technical labels |

Import `fonts.css` from the consuming stylesheet or link it from a template.
Serve it alongside the family directories so relative font URLs resolve.
These are the website's Latin subsets; other scripts, italic faces and additional
static weights are not included. The stylesheet declares faces only: consumers
own font assignments, sizes and layout. No external font service is required.

Reuse [the shared palette](../theme/palette.css) for colors. It deliberately keeps
dark links brighter than filled actions; preserve that distinction. The palette
and these font faces do not select a theme or apply page styling themselves.

Each family retains its exact package's SIL Open Font License and copyright text
in `LICENSE`. `sources.json` records package URLs, versions, registry SHA-512
archive integrity and individual SHA-256 hashes. Downloads were verified against
registry integrity and checked for WOFF2 signatures; no font files were modified.
Distribute the matching licenses with the fonts.

The shared Forgejo styles and canonical staging payload include the fonts and licenses.

The Forgejo redesign uses Barlow Condensed 800 from Fontsource 5.3.0 throughout. Its font-face is declared once in fonts.css. The canonical symbols are copied from the approved Soda website, with an open core and theme-specific middle plate.

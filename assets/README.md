# Reusable Soda assets

Imported from `~/Projects/soda-os` (source HEAD `bc1d3e0dbec48dfaa6a20f9d0453ad3e4cdf353c`). That import is the historical baseline; Forgejo and terminal branding now use the brutalist offset-core symbol. These are design resources, not an implementation of the old product architecture.

## Inventory

| Path | Contents |
| --- | --- |
| `branding/source/` | Current offset-core light/dark symbols and wordmarks |
| `branding/web/` | Web favicons and Apple touch icon |
| `branding/icons/hicolor/` | System icons from 16px to 512px |
| `branding/installer/` | Current light/dark raster logos, export manifest, and Anaconda CSS |
| `branding/cockpit/` | Stock Cockpit branding/theme CSS, retained images/static preview, and the retired workspace's upstream provenance archive |
| `branding/fonts/` | Self-hosted Barlow Condensed, Barlow and IBM Plex Mono, plus retained Fraunces WOFF2 faces, CSS, exact-version provenance and OFL licenses |
| `branding/forgejo/` | Four PNG exports, native theme review sheet, manifest and light/dark/automatic themes; no decorative artwork |
| `branding/theme/palette.css` | Shared light/dark semantic color tokens and control ramps |
| `branding/role-portraits/ps1-v1/` | Ten original PS1-style feed portraits, their approved color reference, gallery and generation prompts; the native Soda extension ships the individual role PNGs |
| `branding/terminal/sodaos.txt` | Canonical ASCII symbol; sibling fastfetch preset and plain MOTD |
| `animated-wave-background/` | Animated SVG background with standalone HTML/CSS demo |

Start with the SVG masters and `branding/theme/palette.css` for new UI work. The palette exposes mode-qualified tokens such as `--soda-light-canvas` and `--soda-dark-action`; it does not select a theme or define typography/spacing. Preserve the distinction between link and filled-action colors.

## Preview locally

Open these files directly in a browser:

- `branding/cockpit/preview.html`
- `animated-wave-background/index.html`

`branding/forgejo/theme-preview.html` is **not standalone**: it requires a matching Forgejo instance and its native CSS/image routes.

## Application-specific styles

These styles have application-specific owners; they are not globally applied to the dashboard. Stock Cockpit staging selects `branding.css` and `theme.css`. The TypeScript/Sass sources and license texts under `branding/cockpit/provenance/` are historical references and are not compiled or staged:

- `branding/cockpit/theme.css`: semantic roles and PatternFly mappings, originally from `cockpit/src/cockpit/theme.css`.
- `branding/cockpit/soda.css`: small Soda page utility styles from the same source directory.
- `branding/cockpit/branding.css`: native Cockpit 366 login adapter, from `packaging/rpm/projects/sources/branding/sodaos/branding.css`.
- `branding/forgejo/css/*.css`: Forgejo 15 themes and controls, from `packaging/rpm/forgejo/sources/custom/public/assets/css/`. Native `theme-forgejo-light.css` / `theme-forgejo-dark.css` are supplied by Forgejo, not this repository.
- `branding/installer/soda.css`: Anaconda integration from `packaging/installer/branding/soda.css`; absolute image URLs refer to installed Anaconda paths.

Palette imports now resolve to the single `branding/theme/palette.css` source. Future packaging must preserve these relative paths or rewrite imports when staging. Native Cockpit staging selects the current light/dark symbols.

The READMEs in `branding/cockpit/` and `branding/theme/` retain original design context. Their descriptions of RPM staging and source-repository tests are historical, not claims about this project.

## Scope and maintenance

No application components, services, OS configuration, dependencies, caches, credentials, or build artifacts were imported. No standalone font files existed outside dependencies/build outputs; use the chosen frontend's fonts rather than copying vendored font infrastructure.

Raster exports use the current offset-core masters. Run `scripts/render-branding.sh` to regenerate installer, desktop, web, Cockpit and Forgejo PNGs, or pass `--check` to compare pixels. The manifests record source files, dimensions and backgrounds.

See [branding usage](../docs/design/branding.md) for mark selection and constraints.

# Public handbook authoring contract

The Factory repository owns this public user handbook; the Soda OS website publishes a pinned snapshot under `/docs/factory`.

## Publication boundary

Only numbered Markdown pages inside this directory are handbook content. This README describes the export contract and is not a published page. Engineering records, research, qualification status and the rest of the repository are outside the public export.

The handbook describes the accepted finished product. Keep current implementation gaps and release qualification in `docs/development/`; do not turn temporary limitations into public semantics.

Use `NN-Section/NN-slug.md` paths. Section and page numbers set the reading order. Every page slug is unique across sections; exactly one `NN-index.md` is the handbook landing page, published at `/docs/factory`; other pages use `/docs/factory/slug`.

## Page structure

Start each page with one H1 title and one plain description paragraph. Use H2 and H3 for content, fenced code blocks with language labels, ordinary Markdown links, tables and lists. No raw HTML, custom components or hidden metadata. Link to other pages with relative `.md` links; external links use HTTPS.

## Wording

Call the product "Factory". Describe coding agents and Git repositories generically; do not name a specific coding-agent service or repository host. Use the interface's own labels for pages and controls.

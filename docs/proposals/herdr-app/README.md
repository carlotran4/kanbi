# Herdr app proposal

Open `proposal.html` in a browser for the self-contained visual document. It has
three illustrative concepts and no runtime integration or external asset needs.
`proposal.md` is its editable narrative; `build.py` contains editable HTML/CSS
concepts. Regenerate with `python3 docs/proposals/herdr-app/build.py` from the repo
root (requires Python Markdown). The captured baseline is bundled for repeatable
builds. The rendered HTML is committed so reading needs no dependencies.

## Validation

- Reviewed rendered HTML in headless Chromium at a 1440-pixel viewport.
- Inspected the real disposable Kanbi Master fixture at 160x45 and 80x24.
- Compared existing topology with the baseline; native concepts deliberately
  change sample content and omit fixture-only Focus groups.
- Stopped the owned fixture. No live database, session or config was modified.
- `git diff --check` passed. No Go behavior changed, so Go tests, vet and smoke
  were not run. These concepts do not certify a working core/app integration.

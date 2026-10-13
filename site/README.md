# Documentation site

Plain static files for Cloudflare Pages: hand-written HTML, one `style.css`, no JavaScript, no build step.

## Cloudflare Pages settings

Connect the repository in the Cloudflare dashboard (**Workers & Pages → Create → Pages → Connect to Git**) with:

| Setting | Value |
|---|---|
| Production branch | `main` |
| Framework preset | None |
| Build command | *(empty)* |
| Build output directory | `site` |

`_headers` sets the security headers (strict CSP: the pages load only `style.css`). `404.html` is served for unknown paths.

## Editing

The pages mirror the Markdown docs in the repository:

| Page | Source |
|---|---|
| `index.html` | `README.md` (intro, license and maintenance) |
| `install.html` | `README.md` (before you start → troubleshooting) |
| `apps.html` | `docs/app-contract.md` |
| `api.html` | `docs/api.md` |
| `internals.html` | `docs/architecture.md` |

When a doc changes, change the matching page in the same pull request. Every page shares the same `<header>` and `<footer>`; keep them identical.

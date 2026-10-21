# OtterMQ management UI

The web interface for the OtterMQ broker, built with [SvelteKit](https://svelte.dev/docs/kit) (Svelte 5, TypeScript, Tailwind CSS). It's a static single-page app that talks to the broker's HTTP API under `/api`.

## Development

Start the broker with the web API enabled (`make run-dev` from the repository root), then:

```sh
npm install
npm run dev            # http://localhost:5173
```

The dev server proxies `/api` to `http://localhost:3000`. If the broker's web API listens elsewhere, set `OTTERMQ_API_URL`:

```sh
OTTERMQ_API_URL=http://localhost:3100 npm run dev
```

Before committing:

```sh
npm run check          # svelte-check (types)
npm run lint           # prettier + eslint
npm run format         # apply prettier
```

## Building

```sh
npm run build          # writes the static site to build/
```

From the repository root, `make ui-build` builds it and copies it to `./ui`, which the broker serves when `OTTERMQ_ENABLE_UI=true` (see `OTTERMQ_UI_PATH`). The broker answers unknown non-API paths with `index.html`, so client-side routes such as `/queues` survive a reload.

## Layout

```
src/
├── routes/
│   ├── login/            login page
│   └── (app)/            pages behind authentication (overview, connections, channels,
│                         exchanges, queues, admin); +layout.ts redirects to /login
└── lib/
    ├── services/api.ts   fetch wrapper: JWT header, errors as ApiError, 401 → logout
    ├── stores/           one module per API area (queues, exchanges, admin, ...)
    ├── pooling.svelte.ts usePolledList: polls a list every 5 s, with refresh()
    ├── components/       Sidebar, Modal, Feedback (toasts + confirm dialog), charts
    └── icons/            SVG icons, imported with ?raw
```

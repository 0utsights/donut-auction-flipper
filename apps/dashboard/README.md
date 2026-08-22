# Donut Network dashboard

The dashboard is a deliberately plain operator console for the DonutSMP market network. It shows only data returned by the backend; when the backend is unavailable, it reports an offline empty state rather than substituting sample data.

## Run locally

```bash
npm ci
npm run dev
```

The browser polls the same-origin `/api/dashboard` route every two seconds. That server route forwards to `DN_BACKEND_URL` with `DN_ADMIN_TOKEN`, keeping the operator credential out of browser JavaScript. Development defaults target `http://localhost:8080` with the documented local token. This dashboard is intended to run locally with the backend.

## Verify

```bash
npm test
npm run lint
npm audit
```

`npm test` performs a production build and checks the rendered page for the required controls and the absence of generated promotional imagery.

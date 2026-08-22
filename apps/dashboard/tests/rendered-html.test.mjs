import assert from "node:assert/strict";
import test from "node:test";

async function render() {
  const workerUrl = new URL("../dist/server/index.js", import.meta.url);
  workerUrl.searchParams.set("test", `${process.pid}-${Date.now()}`);
  const { default: worker } = await import(workerUrl.href);
  const handler = typeof worker === "function" ? worker : worker.fetch.bind(worker);
  return handler(new Request("http://localhost/", { headers: { accept: "text/html" } }), { ASSETS: { fetch: async () => new Response("Not found", { status: 404 }) } }, { waitUntil() {}, passThroughOnException() {} });
}

test("server renders the Donut Network operator dashboard", async () => {
  const response = await render();
  assert.equal(response.status, 200);
  assert.match(response.headers.get("content-type") ?? "", /^text\/html\b/i);
  const html = await response.text();
  assert.match(html, /<title>Donut Network — Market Operations<\/title>/i);
  assert.match(html, /<h1>Overview<\/h1>/);
  assert.match(html, /live data only/);
  assert.match(html, /Recent observations/);
  assert.doesNotMatch(html, /codex-preview|SkeletonPreview|react-loading-skeleton/);
});

test("does not ship generated promotional imagery", async () => {
  const html = await (await render()).text();
  assert.doesNotMatch(html, /og:image|twitter:image|og\.png/);
});

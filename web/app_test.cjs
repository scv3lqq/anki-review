const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");
const source = fs.readFileSync(`${__dirname}/app.js`, "utf8");

function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function harness(fetch = async () => { throw new Error("unexpected fetch"); }) {
  const storage = new Map();
  const context = vm.createContext({
    CSS: { supports: () => true }, AbortController, URL, URLSearchParams, TextEncoder,
    setTimeout, clearTimeout, console, fetch,
    location: { href: "https://review.example/", hash: "" },
    localStorage: { getItem: (key) => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: (key) => storage.delete(key) },
  });
  vm.runInContext(source.split("// ---------- screens ----------")[0] + `\nglobalThis.api = { S, selectAccount, resetAccount, storageKey, save, load, forget, pref, setPref, call, post, invitationLink, StaleAccount };`, context);
  return { api: context.api, context, storage };
}

const account = (id) => ({ account_id: id, is_owner: id === "owner", managed: id !== "owner", multi_account: true, grader_provider: "claude" });

test("drafts and browser preferences are isolated by account even for identical pass IDs", () => {
  const { api, storage } = harness();
  api.selectAccount(account("owner"));
  api.S.session = "same-pass"; api.S.answers = { 1: "owner answer" }; api.save(); api.setPref("batch", 12);
  api.selectAccount(account("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"));
  assert.equal(api.load("same-pass"), null); assert.equal(api.pref("batch"), null);
  api.S.session = "same-pass"; api.S.answers = { 1: "guest answer" }; api.save(); api.setPref("batch", 3);
  api.selectAccount(account("owner"));
  assert.equal(api.load("same-pass").answers[1], "owner answer"); assert.equal(api.pref("batch"), "12");
  assert.equal(storage.size, 4);
  api.forget("same-pass");
  api.selectAccount(account("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"));
  assert.equal(api.load("same-pass").answers[1], "guest answer");
});

test("account reset clears all sensitive review and settings state", () => {
  const { api } = harness();
  api.selectAccount(account("owner"));
  Object.assign(api.S, { session: "s", cards: [{ n: 1 }], answers: { 1: "private" }, results: [{ back: "private" }], grades: { 1: "Good" }, touched: { 1: true }, settings: true, provider: "codex", graderConnected: true, polling: Symbol(), decks: [{ name: "private" }], deck: "private", inviteLink: "secret", knowledgeDecks: ["private"] });
  api.resetAccount();
  for (const name of ["session", "results", "polling", "deck", "accountId"]) assert.equal(api.S[name], null, name);
  for (const name of ["cards", "decks", "knowledgeDecks"]) assert.equal(api.S[name].length, 0, name);
  for (const name of ["answers", "grades", "touched"]) assert.equal(Object.keys(api.S[name]).length, 0, name);
  assert.equal(api.S.settings, false); assert.equal(api.S.authed, false); assert.equal(api.S.inviteLink, ""); assert.equal(api.S.graderConnected, false);
  const size = api.S.session;
  api.save(); assert.equal(size, null);
});

test("fetch completion from a previous account is rejected", async () => {
  const response = deferred(); const { api } = harness(() => response.promise);
  api.selectAccount(account("owner"));
  const request = api.call("api/cards");
  api.selectAccount(account("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"));
  response.resolve({ status: 200, ok: true, json: async () => ({ cards: ["private"] }) });
  await assert.rejects(request, (err) => err.name === "StaleAccount");
});

test("delayed JSON after fetch cannot reach the next account", async () => {
  const body = deferred(); const { api } = harness(async () => ({ status: 200, ok: true, json: () => body.promise }));
  api.selectAccount(account("owner"));
  const response = await api.call("api/results");
  const json = response.json();
  api.selectAccount(account("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"));
  body.resolve({ items: [{ back: "private" }] });
  await assert.rejects(json, (err) => err.name === "StaleAccount");
  assert.equal(api.S.results, null);
});

test("stale unauthorized body cannot log out the new account", async () => {
  const body = deferred(); const { api, context } = harness(async () => ({ status: 401, ok: false, clone: () => ({ json: () => body.promise }) }));
  let logins = 0; context.showLogin = () => { logins++; };
  api.selectAccount(account("owner"));
  const request = api.call("api/cards");
  await Promise.resolve();
  api.selectAccount(account("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"));
  body.resolve({ code: "login" });
  await assert.rejects(request, (err) => err.name === "StaleAccount");
  assert.equal(logins, 0);
});

test("invitation bearer stays in a fragment", () => {
  const { api } = harness();
  const link = new URL(api.invitationLink("opaque-bearer"));
  assert.equal(link.search, ""); assert.equal(link.hash, "#invite=opaque-bearer");
});

class Element {
  constructor(tag = "div") { this.tagName = tag; this.value = ""; this.files = []; this.hidden = false; this.disabled = false; this.dataset = {}; this.children = []; this.attributes = {}; this.textContent = ""; this.classList = { toggle() {} }; this.style = { setProperty() {} }; }
  set value(value) { this._value = value; if (value === "") this.files = []; }
  get value() { return this._value; }
  setAttribute(name, value) { this.attributes[name] = String(value); }
  getAttribute(name) { return this.attributes[name] ?? null; }
  addEventListener() {}
  append(...items) { this.children.push(...items); }
  replaceChildren(...items) { this.children = items; }
  querySelectorAll() { return []; }
  reset() {}
  focus() {}
  set innerHTML(_value) { throw new Error("parent innerHTML is forbidden"); }
}

function pageHarness(fetch, hash = "") {
  const nodes = new Map();
  for (const match of fs.readFileSync(`${__dirname}/index.html`, "utf8").matchAll(/<([a-z][\w-]*)\b[^>]*\bid="([^"]+)"/g)) nodes.set(match[2], new Element(match[1]));
  const get = (id) => { assert.ok(nodes.has(id), `missing page element ${id}`); return nodes.get(id); };
  const location = { href: `https://review.example/${hash}`, hash, reload() {} };
  const context = vm.createContext({
    CSS: { supports: () => true }, AbortController, URL, URLSearchParams, TextEncoder, setTimeout, clearTimeout, console, fetch, location,
    history: { replaceState() { location.hash = ""; } }, navigator: { clipboard: { writeText: async () => {} } }, confirm: () => true,
    localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
    document: { getElementById: get, querySelectorAll: () => [], createElement: (tag) => new Element(tag), createTextNode: (text) => String(text), activeElement: null },
  });
  vm.runInContext(source.replace(/\nboot\(\);\s*$/, "") + `\nglobalThis.api = { S, selectAccount, resetAccount, showSetup, submitSetup, configureAccountUI, loadPreferences, savePreferences, importCards, createInvitation, cardContent, boot, pollResults, post, logout };`, context);
  return { api: context.api, nodes, location, get };
}

const response = (data, status = 200) => ({ status, ok: status >= 200 && status < 300, json: async () => data, clone() { return this; } });

test("invited registration hides infrastructure and submits own credentials", async () => {
  const requests = [];
  const page = pageHarness(async (path, init) => {
    requests.push({ path, init });
    if (path === "api/session") return response({ ...account("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), state: "ready" });
    if (path === "api/cards") return response({ stage: "idle" });
    if (path === "api/decks") return response({ decks: [] });
    return response({});
  }, "#invite=private-invitation");
  const { api, get, location } = page;
  api.S.multiAccount = true; api.showSetup();
  assert.equal(get("setup-anki-field").hidden, true);
  get("setup-user").value = "friend"; get("setup-password").value = "long password value"; get("setup-repeat").value = "long password value"; get("setup-provider").value = "claude";
  await api.submitSetup({ preventDefault() {} });
  assert.equal(requests.length, 0); assert.match(get("setup-claude-error").textContent, /own Claude token/);
  get("setup-claude").value = "sk-ant-oat01-fixture";
  await api.submitSetup({ preventDefault() {} });
  const registration = requests.find((req) => req.path === "api/register");
  assert.ok(registration); const body = JSON.parse(registration.init.body);
  assert.equal(body.invitation, "private-invitation"); assert.equal(body.claude_token, "sk-ant-oat01-fixture"); assert.equal("anki_key" in body, false); assert.equal(location.hash, "");
});

test("package import validates size and sends raw bytes with the preflight header", async () => {
  const requests = [];
  const { api, get } = pageHarness(async (path, init) => { requests.push({ path, init }); return response(path === "api/decks" ? { decks: [] } : {}); });
  api.selectAccount(account("owner")); api.S.authed = true; api.S.screen = "start";
  get("start-import-file").files = [{ name: "cards.apkg", size: (8 << 20) + 1 }];
  await api.importCards("start-import"); assert.equal(requests.length, 0); assert.match(get("start-import-status").textContent, /8 MiB/);
  const file = { name: "cards.apkg", size: 42 }; get("start-import-file").files = [file];
  await api.importCards("start-import");
  const request = requests.find((req) => req.path === "api/import");
  assert.equal(request.init.body, file); assert.equal(request.init.headers["Content-Type"], "application/octet-stream"); assert.equal(request.init.headers["X-Anki-Import"], "1");
});

test("owner invitations and managed settings follow account metadata", async () => {
  const { api, get } = pageHarness(async (path) => response(path === "api/invitations" ? { token: "private-bearer", expires_at: "2030-01-01T00:00:00Z" } : {}));
  api.selectAccount(account("owner")); api.S.authed = true; api.configureAccountUI(); assert.equal(get("invitations").hidden, false);
  await api.createInvitation(); assert.equal(new URL(get("invite-link").value).hash, "#invite=private-bearer");
  api.selectAccount(account("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")); api.S.authed = true; api.configureAccountUI();
  assert.equal(get("invitations").hidden, true); assert.equal(get("set-anki-field").hidden, true); assert.equal(api.S.inviteLink, "");
});

test("knowledge deck preferences are read and saved through the account API", async () => {
  const requests = [];
  const { api, get } = pageHarness(async (path, init = {}) => { requests.push({ path, init }); return response({ knowledge_decks: ["Tech"] }); });
  api.selectAccount(account("owner")); api.S.authed = true;
  await api.loadPreferences(); assert.equal(get("knowledge-decks").value, "Tech");
  get("knowledge-decks").value = "Tech\nScience"; await api.savePreferences({ preventDefault() {} });
  const request = requests.find((req) => req.path === "api/preferences" && req.init.method === "POST");
  assert.deepEqual(JSON.parse(request.init.body), { knowledge_decks: ["Tech", "Science"] });
});

test("rendered card HTML only creates a script-disabled authenticated iframe", () => {
  const { api } = pageHarness(async () => response({})); api.selectAccount(account("owner")); api.S.session = "private pass";
  const frame = api.cardContent({ n: 1, question: "plain", question_html: "<script>hostile</script>" });
  assert.equal(frame.tagName, "iframe"); assert.equal(frame.attributes.sandbox, "allow-same-origin"); assert.equal(frame.attributes.srcdoc, undefined);
  const url = new URL(frame.attributes.src, "https://review.example/");
  assert.equal(url.pathname, "/api/render"); assert.equal(url.searchParams.get("session"), "private pass"); assert.equal(url.searchParams.get("side"), "question");
});

test("boot and result polling reject delayed JSON from the previous account", async () => {
  for (const operation of ["boot", "pollResults"]) {
    const body = deferred();
    const { api } = pageHarness(async () => ({ status: 200, ok: true, json: () => body.promise }));
    api.selectAccount(account("owner")); const pending = api[operation]();
    await Promise.resolve(); await Promise.resolve();
    api.selectAccount(account("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"));
    body.resolve(operation === "boot" ? { ...account("owner"), state: "ready" } : { items: [{ back: "private" }] });
    await pending;
    assert.equal(api.S.accountId, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); assert.equal(api.S.results, null);
  }
});


test("account switch clears visible review data and selected package files", () => {
  const { api, get } = pageHarness(async () => response({}));
  api.selectAccount(account("owner"));
  get("answer").value = "private answer";
  get("knowledge-decks").value = "private deck";
  get("invite-link").value = "private bearer";
  get("start-import-file").files = [{ name: "owner.apkg" }];
  get("set-import-file").files = [{ name: "owner.apkg" }];
  get("res-rows").children = ["private card"];
  api.selectAccount(account("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"));
  for (const id of ["answer", "knowledge-decks", "invite-link"]) assert.equal(get(id).value, "", id);
  for (const id of ["start-import-file", "set-import-file"]) assert.equal(get(id).files.length, 0, id);
  assert.equal(get("res-rows").children.length, 0);
});

test("authenticated calls pin expected account and reject a different response identity", async () => {
  let sent;
  const { api } = harness(async (_path, init) => {
    sent = init;
    return { status: 409, ok: false, headers: { get: () => "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }, json: async () => ({ code: "account_changed" }) };
  });
  api.selectAccount(account("owner")); api.S.answers = { 1: "private owner answer" }; api.S.settings = true;
  await assert.rejects(api.post("api/preferences", { knowledge_decks: ["private"] }), (err) => err.name === "StaleAccount");
  assert.equal(sent.headers["X-Anki-Account"], "owner");
  assert.equal(api.S.accountId, null); assert.equal(api.S.settings, false); assert.equal(Object.keys(api.S.answers).length, 0);
});

test("cross-tab settings conflict reboots the selected session without replaying a mutation", async () => {
  const requests = [];
  const actual = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
  const { api } = pageHarness(async (path, init = {}) => {
    requests.push({ path, init });
    if (path === "api/settings") return { ...response({ code: "account_changed" }, 409), headers: { get: () => actual } };
    if (path === "api/session") return response({ ...account(actual), state: "ready" });
    if (path === "api/cards") return response({ stage: "idle" });
    return response({ decks: [] });
  });
  api.selectAccount(account("owner")); api.S.authed = true;
  await assert.rejects(api.post("api/settings", { password: "owner password" }), (err) => err.name === "StaleAccount");
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(api.S.accountId, actual);
  assert.equal(requests.filter((req) => req.path === "api/settings").length, 1);
  assert.equal(requests[0].init.headers["X-Anki-Account"], "owner");
  assert.equal("X-Anki-Account" in requests.find((req) => req.path === "api/session").init.headers, false);
});

test("logout keeps its expected identity after clearing local state and never logs out the other account", async () => {
  const requests = [];
  const actual = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
  const { api } = pageHarness(async (path, init = {}) => {
    requests.push({ path, init });
    if (path === "api/logout") return { ...response({ code: "account_changed" }, 409), headers: { get: () => actual } };
    if (path === "api/session") return response({ ...account(actual), state: "ready" });
    if (path === "api/cards") return response({ stage: "idle" });
    return response({ decks: [] });
  });
  api.selectAccount(account("owner")); api.S.authed = true; api.S.answers = { 1: "private" };
  await api.logout();
  await new Promise((resolve) => setImmediate(resolve));
  const logout = requests.filter((req) => req.path === "api/logout");
  assert.equal(logout.length, 1); assert.equal(logout[0].init.headers["X-Anki-Account"], "owner");
  assert.equal(api.S.accountId, actual); assert.equal(Object.keys(api.S.answers).length, 0);
});

test("account_changed body also fences legacy responses without an identity header", async () => {
  const { api } = harness(async () => response({ code: "account_changed" }, 409));
  api.selectAccount(account("owner"));
  await assert.rejects(api.post("api/preferences", { knowledge_decks: [] }), (err) => err.name === "StaleAccount");
  assert.equal(api.S.accountId, null);
});

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
  constructor(tag = "div") { this.tagName = tag; this.value = ""; this.files = []; this.hidden = false; this.disabled = false; this.dataset = {}; this.children = []; this.attributes = {}; this.listeners = new Map(); this.textContent = ""; this.classList = { toggle() {} }; this.style = { setProperty() {} }; }
  set value(value) { this._value = value; if (value === "") this.files = []; }
  get value() { return this._value; }
  setAttribute(name, value) { this.attributes[name] = String(value); }
  getAttribute(name) { return this.attributes[name] ?? null; }
  addEventListener(type, listener) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(listener);
  }
  async dispatch(type) { for (const listener of this.listeners.get(type) ?? []) await listener({ type }); }
  append(...items) { this.children.push(...items); }
  replaceChildren(...items) { this.children = items; }
  querySelectorAll() { return []; }
  reset() {}
  focus() {}
  set innerHTML(_value) { throw new Error("parent innerHTML is forbidden"); }
}

const flushPage = () => new Promise((resolve) => setImmediate(resolve));

function pageHarness(fetch, hash = "") {
  const nodes = new Map();
  for (const match of fs.readFileSync(`${__dirname}/index.html`, "utf8").matchAll(/<([a-z][\w-]*)\b[^>]*\bid="([^"]+)"[^>]*>([^<]*)/g)) {
    const node = new Element(match[1]);
    node.textContent = match[3];
    nodes.set(match[2], node);
  }
  const get = (id) => { assert.ok(nodes.has(id), `missing page element ${id}`); return nodes.get(id); };
  let reloads = 0;
  const location = { href: `https://review.example/${hash}`, hash, reload() { reloads++; } };
  const storage = new Map();
  const events = () => {
    const listeners = new Map();
    return {
      addEventListener(type, listener) {
        if (!listeners.has(type)) listeners.set(type, []);
        listeners.get(type).push(listener);
      },
      dispatch(type) { for (const listener of listeners.get(type) ?? []) listener({ type }); },
      listenerCount(type) { return (listeners.get(type) ?? []).length; },
    };
  };
  const window = events();
  const document = { ...events(), hidden: false, getElementById: get, querySelectorAll: () => [], createElement: (tag) => new Element(tag), createTextNode: (text) => String(text), activeElement: null };
  const intervals = new Map();
  let elapsed = 0, nextInterval = 0;
  const setInterval = (callback, delay) => {
    assert.ok(delay > 0, "interval must have a positive delay");
    const id = ++nextInterval;
    intervals.set(id, { callback, delay, next: elapsed + delay });
    return id;
  };
  const clearInterval = (id) => intervals.delete(id);
  Object.assign(window, { setInterval, clearInterval });
  const context = vm.createContext({
    CSS: { supports: () => true }, AbortController, URL, URLSearchParams, TextEncoder, setTimeout, clearTimeout, setInterval, clearInterval, console, fetch, location, window,
    history: { replaceState() { location.hash = ""; } }, navigator: { clipboard: { writeText: async () => {} } }, confirm: () => true,
    localStorage: { getItem: (key) => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, String(value)), removeItem: (key) => storage.delete(key) },
    document,
  });
  vm.runInContext(source.replace(/\nboot\(\);\s*$/, "") + `\nglobalThis.api = { S, selectAccount, resetAccount, showSetup, submitSetup, configureAccountUI, loadPreferences, savePreferences, importCards, createInvitation, cardContent, boot, pollResults, post, logout, showStart, showResults, show, startPass, takeMore, pickDeck, setPref, refreshDueCards: typeof refreshDueCards === "function" ? refreshDueCards : undefined };`, context);
  return {
    api: context.api, nodes, location, get, context, document, window, intervals,
    get reloads() { return reloads; },
    async advance(ms) {
      const end = elapsed + ms;
      while (true) {
        const next = Math.min(...[...intervals.values()].map((timer) => timer.next));
        if (next > end) break;
        elapsed = next;
        for (const timer of [...intervals.values()]) {
          if (timer.next !== next) continue;
          timer.next += timer.delay;
          timer.callback();
        }
        await flushPage();
      }
      elapsed = end;
      await flushPage();
    },
    async dispatch(target, type) { ({ window, document })[target].dispatch(type); await flushPage(); },
  };
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

const techDeck = (due) => ({ name: "Tech", due, fresh: 0 });

function readyStart(page, decks = []) {
  page.api.selectAccount(account("owner"));
  Object.assign(page.api.S, { authed: true, screen: "start", decks });
  page.api.pickDeck(decks[0]?.name ?? null);
}

function writtenResults(page, more = 0) {
  readyStart(page);
  Object.assign(page.api.S, { session: "finished-pass", deck: "Tech", answers: { 1: "typed answer" } });
  page.api.showResults({
    items: [{ n: 1, question: "Question", answer: "typed answer", back: "Key", grade: "Good" }],
    confirmed: { 1: "Good" }, status: { written: true, more, message: "Saved safely." },
  });
}

function refresh(page) {
  assert.equal(typeof page.api.refreshDueCards, "function", "refreshDueCards must be available");
  return page.api.refreshDueCards();
}

test("due refresh discovers an empty start screen on the minute without reloading", async () => {
  const requests = [];
  const page = pageHarness(async (path, init) => { requests.push({ path, init }); return response({ decks: [techDeck(3)] }); });
  readyStart(page);
  assert.equal(page.intervals.size, 1, "install one repeating timer once");
  await page.advance(59999);
  assert.equal(requests.length, 0);
  await page.advance(1);
  assert.equal(requests.length, 1);
  assert.equal(requests[0].path, "api/decks");
  assert.equal(requests[0].init.cache, "no-store");
  assert.equal(requests[0].init.method ?? "GET", "GET");
  assert.equal(page.api.S.deck, "Tech");
  assert.equal(page.api.S.decks[0].due, 3);
  assert.equal(page.get("start-empty").hidden, true);
  assert.equal(page.get("start-count").max, "3");
  assert.equal(page.get("start-go").disabled, false);
  await page.advance(60000);
  assert.equal(requests.length, 2);
  assert.equal(page.intervals.size, 1);
  assert.equal(page.reloads, 0);
});

for (const [target, event] of [["window", "focus"], ["document", "visibilitychange"], ["window", "pageshow"], ["window", "online"]]) {
  test(`due refresh discovers new cards on ${event}`, async () => {
    let calls = 0;
    const page = pageHarness(async (path) => { assert.equal(path, "api/decks"); calls++; return response({ decks: [techDeck(3)] }); });
    readyStart(page);
    await page.dispatch(target, event);
    assert.equal(calls, 1);
    assert.equal(page.api.S.deck, "Tech");
    assert.equal(page.api.S.decks[0].due, 3);
    assert.equal(page.reloads, 0);
  });
}

test("due refresh preserves selected deck and typed batch count", async () => {
  const page = pageHarness(async () => response({ decks: [{ name: "English", due: 9 }, techDeck(5)] }));
  readyStart(page, [techDeck(3)]);
  page.get("start-count").value = "2";
  await refresh(page);
  assert.equal(page.api.S.deck, "Tech");
  assert.equal(page.get("start-count").value, "2");
  assert.equal(page.get("start-count").max, "5");
  assert.equal(page.get("start-go").disabled, false);
});

for (const preferred of [null, "Science"]) {
  test(`due refresh replaces a deleted selection using ${preferred ? "saved preference" : "the first due deck"}`, async () => {
    const page = pageHarness(async () => response({ decks: [{ name: "English", due: 4 }, { name: "Science", due: 2 }] }));
    readyStart(page, [techDeck(3)]);
    if (preferred) page.api.setPref("deck", preferred);
    await refresh(page);
    assert.equal(page.api.S.deck, preferred ?? "English");
    assert.equal(page.get("start-count").value, preferred ? "2" : "4");
  });
}

test("due refresh updates written More availability without rebuilding results or changing answers", async () => {
  let decks = [techDeck(3), { name: "English", due: 12 }];
  const page = pageHarness(async () => response({ decks }));
  writtenResults(page);
  const { S } = page.api;
  const rows = page.get("res-rows").children.slice();
  const results = S.results, grades = S.grades, answers = S.answers;
  const snapshot = JSON.stringify({ results, grades, answers, message: S.message, session: S.session });
  await refresh(page);
  assert.equal(S.more, 3);
  assert.equal(page.get("take").hidden, false);
  assert.equal(page.get("to-decks").hidden, true);
  assert.equal(page.get("take-count").max, "3");
  assert.equal(page.get("take-count").value, "3");
  assert.equal(page.get("more").disabled, false);
  page.get("take-count").value = "2";
  await refresh(page);
  assert.equal(page.get("take-count").value, "2", "preserve edited count even after blur");
  decks = [techDeck(0), { name: "English", due: 12 }];
  await refresh(page);
  assert.equal(S.more, 0);
  decks = [{ name: "English", due: 12 }];
  await refresh(page);
  assert.equal(S.more, 0, "missing selected deck is empty, regardless of other decks");
  assert.equal(page.get("take").hidden, true);
  assert.equal(page.get("to-decks").hidden, false);
  assert.equal(page.get("take-count").max, "0");
  assert.equal(page.get("more").disabled, true);
  assert.equal(S.results, results); assert.equal(S.grades, grades); assert.equal(S.answers, answers);
  assert.equal(page.get("res-rows").children.length, rows.length);
  rows.forEach((row, index) => assert.equal(page.get("res-rows").children[index], row));
  assert.equal(JSON.stringify({ results: S.results, grades: S.grades, answers: S.answers, message: S.message, session: S.session }), snapshot);
  assert.equal(page.get("res-hint").textContent, "Saved safely.");
  assert.equal(page.reloads, 0);
});

test("due refresh never overwrites a focused More count when availability first appears", async () => {
  const page = pageHarness(async () => response({ decks: [techDeck(3)] }));
  writtenResults(page);
  page.document.activeElement = page.get("take-count");
  page.get("take-count").value = "2";
  await refresh(page);
  assert.equal(page.get("take-count").value, "2");
  assert.equal(page.get("take-count").max, "3");
});

test("due refresh does not request decks while hidden or ineligible", async () => {
  const cases = [
    { hidden: true }, { authed: false }, { screen: "input" }, { screen: "checking" },
    { screen: "results", written: false }, { screen: "boot" }, { screen: "login" }, { screen: "setup" }, { screen: "error" }, { settings: true }, { starting: true },
    { taking: true }, { importing: true }, { decksLoading: true },
  ];
  for (const state of cases) {
    let calls = 0;
    const page = pageHarness(async () => { calls++; return response({ decks: [] }); });
    readyStart(page);
    Object.assign(page.api.S, state);
    page.document.hidden = Boolean(state.hidden);
    await refresh(page);
    await page.advance(60000);
    for (const [target, event] of [["window", "focus"], ["window", "online"], ["window", "pageshow"], ["document", "visibilitychange"]]) await page.dispatch(target, event);
    assert.equal(calls, 0, JSON.stringify(state));
  }
});

test("due refresh waits for the initial showStart deck load", async () => {
  const body = deferred(); let calls = 0;
  const page = pageHarness(async () => { calls++; return { ...response({}), json: () => body.promise }; });
  readyStart(page);
  const initial = page.api.showStart();
  await flushPage();
  assert.equal(page.api.S.decksLoading, true);
  await page.advance(60000);
  await page.dispatch("window", "focus");
  assert.equal(calls, 1);
  body.resolve({ decks: [techDeck(3)] });
  await initial;
  assert.equal(page.api.S.decksLoading, false);
  assert.equal(page.api.S.deck, "Tech");
});

test("due refresh coalesces a minute tick and concurrent resume events", async () => {
  const body = deferred(); let calls = 0;
  const page = pageHarness(async () => { calls++; return { ...response({}), json: () => body.promise }; });
  readyStart(page);
  await page.advance(60000);
  await page.dispatch("window", "focus");
  await page.dispatch("window", "online");
  assert.equal(calls, 1);
  body.resolve({ decks: [techDeck(3)] });
  await flushPage();
  assert.equal(page.api.S.deck, "Tech");
});

for (const change of ["account", "session", "screen", "settings", "starting", "taking", "importing", "decksLoading", "hidden"]) {
  test(`due refresh discards delayed JSON after ${change} changes`, async () => {
    const body = deferred();
    const page = pageHarness(async () => ({ ...response({}), json: () => body.promise }));
    readyStart(page, [techDeck(2)]);
    const pending = refresh(page);
    await flushPage();
    if (change === "account") { page.api.selectAccount(account("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")); page.api.S.authed = true; }
    else if (change === "session") page.api.S.session = "another-pass";
    else if (change === "screen") page.api.S.screen = "checking";
    else if (change === "hidden") page.document.hidden = true;
    else page.api.S[change] = true;
    const decks = page.api.S.decks;
    const count = page.get("start-count").value;
    body.resolve({ decks: [techDeck(99)] });
    await pending;
    assert.equal(page.api.S.decks, decks);
    assert.equal(page.get("start-count").value, count);
  });
}

test("due refresh invalidates responses across a screen departure and return", async () => {
  const body = deferred();
  const page = pageHarness(async () => ({ ...response({}), json: () => body.promise }));
  readyStart(page, [techDeck(2)]);
  const pending = refresh(page);
  await flushPage();
  page.api.S.settings = true; page.api.show();
  page.api.S.settings = false; page.api.show();
  body.resolve({ decks: [techDeck(99)] });
  await pending;
  assert.equal(page.api.S.decks[0].due, 2);
});

for (const operation of ["startPass", "takeMore", "importCards", "showStart"]) {
  test(`due refresh discards delayed JSON after ${operation} enters and exits busy state`, async () => {
    const body = deferred(); let deckCalls = 0;
    const page = pageHarness(async (path) => {
      if (path === "api/decks" && ++deckCalls === 1) return { ...response({}), json: () => body.promise };
      if (path === "api/decks") return response({ decks: [techDeck(2)] });
      if (operation === "importCards") throw new Error("Import failed");
      return response({ error: "Temporarily unavailable" }, 500);
    });
    if (operation === "takeMore") writtenResults(page, 2);
    else readyStart(page, [techDeck(2)]);
    const pending = refresh(page);
    await flushPage();
    if (operation === "importCards") page.get("start-import-file").files = [{ name: "fixture.apkg", size: 42 }];
    await page.api[operation](...(operation === "importCards" ? ["start-import"] : []));
    assert.equal(page.api.S.starting, false);
    assert.equal(page.api.S.taking, false);
    assert.equal(page.api.S.importing, false);
    const decks = page.api.S.decks, more = page.api.S.more;
    const hint = page.get(operation === "takeMore" ? "res-hint" : "start-hint").textContent;
    body.resolve({ decks: [techDeck(99)] });
    await pending;
    assert.equal(page.api.S.decks, decks);
    assert.equal(page.api.S.more, more);
    assert.equal(page.get(operation === "takeMore" ? "res-hint" : "start-hint").textContent, hint);
  });
}

test("old due refresh completion cannot clear a replacement request token", async () => {
  const first = deferred(), second = deferred(); let calls = 0;
  const page = pageHarness(async () => ({ ...response({}), json: () => (++calls === 1 ? first.promise : second.promise) }));
  readyStart(page, [techDeck(2)]);
  const old = refresh(page);
  await flushPage();
  page.api.show();
  const current = refresh(page);
  await flushPage();
  assert.equal(calls, 2);
  const token = page.api.S.dueRefresh;
  assert.ok(token);
  first.resolve({ decks: [techDeck(99)] });
  await old;
  assert.equal(page.api.S.dueRefresh, token);
  await page.dispatch("window", "focus");
  assert.equal(calls, 2);
  second.resolve({ decks: [techDeck(3)] });
  await current;
  assert.equal(page.api.S.decks[0].due, 3);
  assert.equal(page.api.S.dueRefresh, null);
});

for (const screen of ["start", "results"]) {
  test(`due refresh preserves ${screen} UI on failure and recovers on a later tick`, async () => {
    let calls = 0;
    const page = pageHarness(async () => { if (++calls === 1) throw new Error("Network failure"); return response({ decks: [techDeck(3)] }); });
    if (screen === "results") writtenResults(page);
    else { readyStart(page, [techDeck(2)]); page.api.S.message = "Keep this message"; page.api.pickDeck("Tech"); }
    const decks = page.api.S.decks;
    const hint = page.get(screen === "start" ? "start-hint" : "res-hint").textContent;
    const message = page.api.S.message;
    await page.advance(60000);
    assert.equal(calls, 1);
    assert.equal(page.api.S.decks, decks);
    assert.equal(page.api.S.message, message);
    assert.equal(page.get(screen === "start" ? "start-hint" : "res-hint").textContent, hint);
    await page.advance(60000);
    assert.equal(calls, 2);
    assert.equal(screen === "start" ? page.api.S.decks[0].due : page.api.S.more, 3);
  });
}

test("boot retains the finished deck for live More availability", async () => {
  const page = pageHarness(async (path) => {
    if (path === "api/session") return response({ ...account("owner"), state: "ready" });
    if (path === "api/cards") return response({ session: "finished-pass", deck: "Tech", stage: "written", cards: [], due: 0 });
    if (path === "api/results") return response({ items: [], confirmed: {}, status: { written: true, more: 0 } });
    assert.equal(path, "api/decks");
    return response({ decks: [techDeck(3)] });
  });
  await page.api.boot();
  assert.equal(page.api.S.deck, "Tech");
  await refresh(page);
  assert.equal(page.api.S.more, 3);
});

test("account reset clears pending due refresh and initial deck loading", () => {
  const page = pageHarness(async () => response({ decks: [] }));
  readyStart(page);
  Object.assign(page.api.S, { dueRefresh: {}, decksLoading: true });
  page.api.resetAccount();
  assert.equal(page.api.S.dueRefresh, null);
  assert.equal(page.api.S.decksLoading, false);
});


const interruptedResult = (paused = true) => ({
  session: "saved-pass", paused,
  items: [{ n: 1, question: "Question", answer: "saved answer", back: "Key", grade: "Good" }],
  confirmed: { 1: "Hard" }, status: { written: false, more: 0 },
});

function pausedPage(stage, fetch) {
  const requests = [];
  const page = pageHarness(async (path, init = {}) => {
    requests.push({ path, init });
    if (path === "api/session") return response({ ...account("owner"), state: "ready" });
    if (path === "api/cards") return response({ session: "saved-pass", deck: "Tech", stage, paused: true, cards: [{ n: 1, question: "Question" }], answers: [{ n: 1, answer: "saved answer" }], due: 1 });
    return fetch(path, init);
  });
  return { page, requests };
}

for (const stage of ["checking", "confirmed"]) {
  test(`paused ${stage} boot waits for an explicit Resume click and preserves saved data`, async (t) => {
    let resumed = false;
    const { page, requests } = pausedPage(stage, async (path) => {
      if (path === "api/resume") { resumed = true; return response({}); }
      assert.equal(path, "api/results");
      return response(stage === "checking" ? { session: "saved-pass", items: [{ n: 1, question: "Question", answer: "saved answer", back: "Key", grade: "Good" }] } : { ...interruptedResult(!resumed), status: { written: resumed } });
    });
    t.after(() => page.api.resetAccount());
    await page.api.boot();
    assert.equal(requests.filter((req) => req.init.method === "POST").length, 0);
    assert.equal(page.api.S.polling, null);
    assert.equal(page.api.S.answers[1], "saved answer");
    const action = page.get(stage === "checking" ? "resume-checking" : "resume-saving");
    assert.equal(action.hidden, false);
    assert.equal(action.textContent, stage === "checking" ? "Resume checking" : "Resume saving to Anki");
    if (stage === "checking") {
      assert.equal(page.get("pulse").hidden, true);
      assert.equal(page.api.S.results, null, "checking pause never renders empty results or answer keys");
    } else {
      assert.equal(page.api.S.grades[1], "Hard");
      assert.equal(page.api.S.results[0].back, "Key");
      assert.equal(page.get("write").hidden, true);
    }
    await action.dispatch("click");
    await flushPage();
    const resumes = requests.filter((req) => req.path === "api/resume");
    assert.equal(resumes.length, 1);
    assert.equal(resumes[0].init.method, "POST");
    assert.deepEqual(JSON.parse(resumes[0].init.body), { session: "saved-pass" });
    assert.equal(page.api.S.screen, "results");
    assert.equal(page.api.S.paused, false);
    assert.equal(page.api.S.polling, null);
    assert.equal(page.api.S.answers[1], "saved answer");
    if (stage === "confirmed") assert.equal(page.api.S.written, true);
  });
}

test("an open checking tab stops polling on a paused response without rendering answer keys", async (t) => {
  const requests = [];
  const page = pageHarness(async (path, init = {}) => {
    requests.push({ path, init });
    return response({ session: "saved-pass", paused: true, items: [] });
  });
  t.after(() => page.api.resetAccount());
  readyStart(page);
  Object.assign(page.api.S, { screen: "checking", session: "saved-pass", answers: { 1: "typed answer" } });
  await page.api.pollResults();
  assert.equal(page.api.S.screen, "checking");
  assert.equal(page.api.S.results, null);
  assert.equal(page.api.S.polling, null);
  assert.equal(page.get("resume-checking").hidden, false);
  assert.equal(page.get("pulse").hidden, true);
  assert.equal(requests.length, 1);
  assert.equal(requests[0].init.method, undefined);
  assert.equal(page.api.S.answers[1], "typed answer");
});

test("an open saving tab detects paused results and offers Resume without changing confirmed grades", async (t) => {
  const requests = [];
  const page = pageHarness(async (path, init = {}) => {
    requests.push({ path, init });
    return response(path === "api/results" ? interruptedResult() : { written: true });
  });
  t.after(() => page.api.resetAccount());
  readyStart(page);
  page.api.S.session = "saved-pass";
  page.api.showResults(interruptedResult(false));
  await flushPage();
  assert.equal(page.api.S.paused, true);
  assert.equal(page.api.S.polling, null);
  assert.equal(page.api.S.written, false);
  assert.equal(page.api.S.grades[1], "Hard");
  assert.equal(page.get("resume-saving").hidden, false);
  assert.equal(page.get("write").hidden, true);
  assert.equal(requests.length, 1);
  assert.equal(requests[0].path, "api/results");
  assert.equal(requests[0].init.method, undefined);
});

for (const stage of ["checking", "confirmed"]) {
  test(`paused ${stage} Resume fences duplicate clicks and delayed completions after an account change`, async (t) => {
    const completion = deferred();
    const { page, requests } = pausedPage(stage, async (path) => path === "api/resume" ? completion.promise : response(interruptedResult()));
    t.after(() => page.api.resetAccount());
    await page.api.boot();
    const action = page.get(stage === "checking" ? "resume-checking" : "resume-saving");
    const pending = action.dispatch("click");
    await flushPage();
    assert.equal(action.disabled, true);
    await action.dispatch("click");
    assert.equal(requests.filter((req) => req.path === "api/resume").length, 1);
    page.api.selectAccount(account("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"));
    Object.assign(page.api.S, { screen: "start", message: "new account" });
    completion.resolve(response({}));
    await pending;
    assert.equal(page.api.S.screen, "start");
    assert.equal(page.api.S.session, null);
    assert.equal(page.api.S.results, null);
    assert.equal(page.api.S.paused, false);
    assert.equal(page.api.S.message, "new account");
    assert.equal(requests.filter((req) => req.path === "api/resume").length, 1);
    assert.equal(requests.filter((req) => req.path === "api/results").length, stage === "confirmed" ? 1 : 0);
  });
}

test("failed Resume stays paused and preserves the pass for an explicit retry", async (t) => {
  const { page, requests } = pausedPage("checking", async (path) => {
    if (path === "api/results") return response({ items: [] });
    assert.equal(path, "api/resume");
    return response({ error: "Resume temporarily unavailable" }, 500);
  });
  t.after(() => page.api.resetAccount());
  await page.api.boot();
  await page.get("resume-checking").dispatch("click");
  assert.equal(page.api.S.paused, true);
  assert.equal(page.api.S.polling, null);
  assert.equal(page.api.S.session, "saved-pass");
  assert.equal(page.api.S.answers[1], "saved answer");
  assert.equal(page.get("resume-checking").disabled, false);
  assert.match(page.get("check-sub").textContent, /Resume temporarily unavailable/);
  assert.equal(requests.filter((req) => req.path === "api/resume").length, 1);
});

for (const retry of [false, true]) {
  test(`saving polls read the results status envelope and ${retry ? "unlock failed writes" : "retain rendered rows on completion"}`, async (t) => {
    const requests = [];
    const page = pageHarness(async (path, init = {}) => {
      requests.push({ path, init });
      return response({ ...interruptedResult(false), status: { written: !retry, retry, message: retry ? "No grades saved. Try again." : "Saved safely.", more: 2 } });
    });
    t.after(() => page.api.resetAccount());
    readyStart(page);
    page.api.S.session = "saved-pass";
    page.api.showResults(interruptedResult(false));
    const row = page.get("res-rows").children[0];
    await flushPage();
    assert.equal(requests.length, 1);
    assert.equal(requests[0].path, "api/results");
    assert.equal(requests[0].init.method, undefined);
    assert.equal(page.api.S.polling, null);
    assert.equal(page.api.S.grades[1], "Hard");
    assert.equal(page.api.S.written, !retry);
    assert.equal(page.api.S.locked, !retry);
    assert.equal(page.api.S.more, 2);
    assert.equal(page.get("res-hint").textContent, retry ? "No grades saved. Try again." : "Saved safely.");
    if (retry) assert.equal(page.get("write").disabled, false);
    else assert.equal(page.get("res-rows").children[0], row);
  });
}

test("Resume with an expired pass reloads state without polling or sending it again", async (t) => {
  const { page, requests } = pausedPage("checking", async (path) => {
    assert.equal(path, "api/resume");
    return response({ code: "session" }, 409);
  });
  t.after(() => page.api.resetAccount());
  await page.api.boot();
  await page.get("resume-checking").dispatch("click");
  assert.equal(page.reloads, 1);
  assert.equal(page.api.S.polling, null);
  assert.equal(requests.filter((req) => req.path === "api/resume").length, 1);
  assert.equal(requests.filter((req) => req.path === "api/results").length, 0);
});

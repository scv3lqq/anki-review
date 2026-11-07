"use strict";
const GRADES = ["Again", "Hard", "Good", "Easy"];
const POLL_MS = 1500;
const DUE_REFRESH_MS = 60000;
const STORE = "anki-review";
const DEFAULT_BATCH = 25;
const FIELD_SIZING = CSS.supports("field-sizing", "content");
const $ = (id) => document.getElementById(id);
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const pad = (n) => String(n).padStart(2, "0");

const initialState = () => ({
  session: null, cards: [], screen: "boot", view: "card", idx: 0,
  answers: {}, touched: {}, grades: {}, open: null,
  results: null, locked: false, written: false, message: "", polling: null, more: 0, taking: false,
  decks: [], deck: null, starting: false, decksLoading: false, dueRefresh: null, gradeError: "",
  authed: false, settings: false, provider: "claude", graderConnected: false,
  accountId: null, isOwner: false, managed: false, multiAccount: false,
  knowledgeDecks: [], inviteLink: "", authBusy: false, importing: false,
});
const S = initialState();
let accountEpoch = 0;
let accountRequests = new AbortController();
let invitation = new URLSearchParams(location.hash.slice(1)).get("invite") || "";

class StaleAccount extends Error {
  constructor() { super("Account changed"); this.name = "StaleAccount"; }
}

const currentAccount = (epoch) => epoch === accountEpoch;
function assertCurrent(epoch) { if (!currentAccount(epoch)) throw new StaleAccount(); }
const interrupted = (epoch, error) => !currentAccount(epoch) || error instanceof StaleAccount || error instanceof LoginRequired;

function resetAccount(id = null) {
  accountRequests.abort();
  accountRequests = new AbortController();
  accountEpoch++;
  const multiAccount = S.multiAccount;
  Object.assign(S, initialState(), { accountId: id, multiAccount });
  if (typeof document !== "undefined") clearAccountDOM();
}

function selectAccount(info) {
  resetAccount(info.account_id || "owner");
  S.multiAccount = Boolean(info.multi_account);
  S.isOwner = info.is_owner ?? S.accountId === "owner";
  S.managed = Boolean(info.managed);
  S.provider = info.grader_provider || "claude";
  S.graderConnected = Boolean(info.grader_connected);
}

const storageKey = (name) => S.accountId ? `${STORE}:${S.accountId}:${name}` : null;
function invitationLink(token) {
  const link = new URL(location.href);
  link.search = "";
  link.hash = new URLSearchParams({ invite: token }).toString();
  return link.href;
}

function el(tag, attrs = {}, ...kids) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (key === "class") node.className = value;
    else if (key.startsWith("on")) node.addEventListener(key.slice(2), value);
    else if (value === true) node.setAttribute(key, "");
    else if (value !== false && value != null) node.setAttribute(key, value);
  }
  for (const kid of kids.flat()) if (kid != null && kid !== false) node.append(kid);
  return node;
}

// `code` in card text renders as inline code; unbalanced backticks stay plain text
function rich(text) {
  const parts = String(text ?? "").split("`");
  if (parts.length % 2 === 0) return [document.createTextNode(text)];
  return parts.map((part, i) => (i % 2 ? el("code", {}, part) : document.createTextNode(part)));
}

const plural = (n, one, many = `${one}s`) => (n === 1 ? one : many);

function autosize(ta) {
  if (FIELD_SIZING) return;
  ta.style.height = "auto";
  ta.style.height = `${ta.scrollHeight + 2}px`;
}

const failure = (err) => (err instanceof TypeError ? "Server not responding." : err.message);

// ---------- storage: survives a reload, never required ----------

function save() {
  if (!S.session || !S.accountId) return;
  const data = { answers: S.answers, touched: S.touched, idx: S.idx, view: S.view, grades: S.grades };
  try {
    localStorage.setItem(storageKey(S.session), JSON.stringify(data));
  } catch {}
}

function load(session) {
  if (!S.accountId) return null;
  try {
    return JSON.parse(localStorage.getItem(storageKey(session)) || "null");
  } catch {
    return null;
  }
}

function forget(session) {
  if (!S.accountId) return;
  try {
    localStorage.removeItem(storageKey(session));
  } catch {}
}

function pref(name) {
  if (!S.accountId) return null;
  try {
    return localStorage.getItem(storageKey(name));
  } catch {
    return null;
  }
}

function setPref(name, value) {
  if (!S.accountId) return;
  try {
    localStorage.setItem(storageKey(name), String(value));
  } catch {}
}

const validCount = (value, max) => /^\d+$/.test(String(value).trim()) && Number(value) >= 1 && Number(value) <= max;
const clampCount = (n, max) => Math.min(Math.max(Math.round(n) || 1, 1), max);

// the last size I picked, else the default
const batchSize = (max) => clampCount(Number(pref("batch")) || DEFAULT_BATCH, max);

// ---------- answers ----------

const norm = (answer) => {
  const a = String(answer ?? "").trim();
  return a === "-" ? "" : a;
};

function cardState(n) {
  const a = norm(S.answers[n]);
  if (a === "?") return "forgot";
  if (a) return "answered";
  return S.touched[n] ? "skipped" : "pending";
}

const filled = () => S.cards.filter((c) => norm(S.answers[c.n])).length;

// a 401 "login" means the session is gone: the login screen takes over and the caller's loop stops
class LoginRequired extends Error {}

// Fence both fetch and body consumption. A response can arrive before an account switch while
// its streamed JSON finishes afterward; neither completion may update the next account.
function emptyJSON(error) { if (error instanceof StaleAccount) throw error; return {}; }
function accountChanged() {
  resetAccount();
  if (typeof document !== "undefined") { S.screen = "boot"; show(); boot(); }
  throw new StaleAccount();
}

function verifyResponseAccount(response, expected, epoch) {
  assertCurrent(epoch);
  const actual = response.headers?.get?.("X-Anki-Account");
  if (expected && actual && actual !== expected) accountChanged();
}

function fencedResponse(response, epoch, expected) {
  return new Proxy(response, { get(target, key) {
    if (key === "json") return async () => {
      try { const data = await target.json(); verifyResponseAccount(target, expected, epoch); return data; }
      catch (error) { verifyResponseAccount(target, expected, epoch); throw error; }
    };
    if (key === "clone") return () => fencedResponse(target.clone(), epoch, expected);
    const value = Reflect.get(target, key, target);
    return typeof value === "function" ? value.bind(target) : value;
  } });
}

async function call(path, init = {}) {
  const epoch = accountEpoch;
  const bootstrap = ["api/session", "api/setup", "api/register", "api/login"].includes(path);
  const headers = { ...init.headers };
  const expected = bootstrap ? null : headers["X-Anki-Account"] || S.accountId;
  if (expected) headers["X-Anki-Account"] = expected;
  else delete headers["X-Anki-Account"];
  let response;
  try { response = await fetch(path, { ...init, headers, signal: accountRequests.signal }); }
  catch (error) { assertCurrent(epoch); throw error; }
  verifyResponseAccount(response, expected, epoch);
  const res = fencedResponse(response, epoch, expected);
  if (res.status === 401 || res.status === 409) {
    const reply = await res.clone().json().catch(emptyJSON);
    assertCurrent(epoch);
    if (reply.code === "account_changed") accountChanged();
    if (res.status === 401 && reply.code === "login") { showLogin(); throw new LoginRequired(); }
  }
  return res;
}

function responseError(res, reply) {
  return Object.assign(new Error(reply.error ?? `HTTP ${res.status}`), { status: res.status, code: reply.code, field: reply.field });
}

async function requestJSON(path, init = {}) {
  const epoch = accountEpoch;
  const res = await call(path, init);
  assertCurrent(epoch);
  const reply = await res.json().catch(emptyJSON);
  assertCurrent(epoch);
  if (!res.ok) throw responseError(res, reply);
  return reply;
}

// "ok", or the conflict code returned by an account's current review pass.
async function post(path, body, expected = S.accountId) {
  const epoch = accountEpoch;
  const headers = { "Content-Type": "application/json" };
  if (expected) headers["X-Anki-Account"] = expected;
  const res = await call(path, { method: "POST", headers, body: JSON.stringify(body) });
  assertCurrent(epoch);
  if (res.ok) return "ok";
  const reply = await res.json().catch(emptyJSON);
  assertCurrent(epoch);
  if (res.status === 409 && ["sent", "session", "empty", "busy"].includes(reply.code)) return reply.code;
  throw responseError(res, reply);
}

function cardContent(item, side = "question") {
  if (!item[`${side === "back" ? "back" : "question"}_html`]) return el("span", {}, ...rich(item[side]));
  return el("iframe", {
    class: "card-frame", title: side === "back" ? "Card answer key" : "Card question",
    src: `api/render?${new URLSearchParams({ session: S.session, n: item.n, side })}`,
    sandbox: "allow-same-origin", loading: "lazy", referrerpolicy: "no-referrer",
  });
}

// ---------- screens ----------

function canLeave() {
  if (S.screen === "input" || S.screen === "results") return !S.locked || S.written;
  return S.screen === "checking" && Boolean(S.gradeError);
}

function show() {
  S.dueRefresh = null;
  // settings cover the screen underneath, so a poll that lands meanwhile can't pull the user away
  const active = S.settings ? "settings" : S.screen;
  const input = active === "input";
  $("tabs").hidden = !input;
  for (const b of $("tabs").querySelectorAll("button")) b.setAttribute("aria-pressed", String(b.dataset.view === S.view));
  $("back").hidden = S.settings || !canLeave();
  $("settings-btn").hidden = !S.authed || S.settings;
  $("logout").hidden = !S.authed;
  $("start").hidden = active !== "start";
  $("start-bar").hidden = active !== "start";
  $("card-view").hidden = !(input && S.view === "card");
  $("list-view").hidden = !(input && S.view === "list");
  $("list-bar").hidden = !(input && S.view === "list");
  $("checking").hidden = active !== "checking";
  $("results").hidden = active !== "results";
  $("res-bar").hidden = active !== "results";
  $("error").hidden = active !== "error";
  $("setup").hidden = active !== "setup";
  $("login").hidden = active !== "login";
  $("settings").hidden = active !== "settings";
  if (input && S.view === "card") renderCard();
  if (input && S.view === "list") renderList();
  configureAccountUI();
}

function setView(view) {
  if (S.screen !== "input" || view === S.view) return;
  if (S.view === "card") commit();
  S.view = view;
  save();
  show();
}

async function backToDecks() {
  const epoch = accountEpoch;
  if (S.screen === "input" && filled() > 0 && !confirm("Drop this batch? Your typed answers will be lost.")) return;
  if (S.screen === "results" && !S.written && !confirm("Grades aren't saved to Anki yet. Drop this batch?")) return;
  try {
    // any outcome ends in a reload: it shows whatever the server holds now
    const outcome = await post("api/reset", { session: S.session });
    assertCurrent(epoch);
    if (outcome === "ok") forget(S.session);
  } catch (error) { if (interrupted(epoch, error)) return; }
  if (currentAccount(epoch)) location.reload();
}

$("back").addEventListener("click", backToDecks);
$("to-decks").addEventListener("click", backToDecks);

// ---------- start: deck list ----------

async function showStart() {
  const epoch = accountEpoch;
  S.decksLoading = true;
  S.screen = "start";
  S.session = null;
  S.message = "";
  $("meta").textContent = "";
  show();
  $("start-hint").textContent = "Loading decks…";
  $("start-go").disabled = true;
  try {
    const res = await call("api/decks", { cache: "no-store" });
    const data = await res.json().catch(emptyJSON);
    assertCurrent(epoch);
    if (!res.ok) throw new Error(data.error ?? `HTTP ${res.status}`);
    S.decks = data.decks ?? [];
  } catch (err) {
    if (interrupted(epoch, err)) return;
    S.decks = [];
    S.message = failure(err);
  } finally {
    if (currentAccount(epoch)) S.decksLoading = false;
  }
  const saved = pref("deck");
  pickDeck(S.decks.some((d) => d.name === saved) ? saved : (S.decks[0]?.name ?? null));
}

function pickDeck(name) {
  S.deck = name;
  const deck = S.decks.find((d) => d.name === name);
  if (deck) $("start-count").value = String(batchSize(deck.due));
  renderStart();
}

function renderStart() {
  $("decks").replaceChildren(...S.decks.map((d) => {
    const counts = [`${d.due} due`, d.fresh ? `${d.fresh} new` : null].filter(Boolean).join(" · ");
    const button = el("button", {
      type: "button",
      class: "deck",
      "aria-pressed": String(d.name === S.deck),
      onclick: () => pickDeck(d.name),
    }, el("span", { class: "dname" }, d.name.split("::").at(-1)), el("span", { class: "dcount" }, counts));
    button.style.setProperty("--depth", String(d.name.split("::").length - 1));
    return el("li", {}, button);
  }));
  $("start-empty").hidden = S.decks.length > 0 || Boolean(S.message);
  const deck = S.decks.find((d) => d.name === S.deck);
  const input = $("start-count");
  input.max = String(deck?.due ?? 1);
  input.disabled = !deck || S.starting;
  $("start-go").disabled = !deck || S.starting || !validCount(input.value, deck.due);
  $("start-go").textContent = S.starting ? "Taking cards…" : "Start";
  const hint = $("start-hint");
  hint.classList.toggle("bad", Boolean(S.message));
  hint.textContent = S.message || (deck ? `${deck.name}: how many cards to take?` : "");
}

function canRefreshDueCards() {
  return S.authed && !document.hidden && !S.settings && !S.starting && !S.taking &&
    !S.importing && !S.decksLoading && (S.screen === "start" || (S.screen === "results" && S.written));
}

async function refreshDueCards() {
  if (!canRefreshDueCards() || S.dueRefresh) return;
  const epoch = accountEpoch;
  const screen = S.screen, session = S.session;
  const token = (S.dueRefresh = Symbol("due-refresh"));
  try {
    const data = await requestJSON("api/decks", { cache: "no-store" });
    if (!currentAccount(epoch) || S.dueRefresh !== token || S.screen !== screen ||
        S.session !== session || !canRefreshDueCards()) return;
    const decks = data.decks ?? [];
    if (screen === "start") {
      S.decks = decks;
      if (decks.some((deck) => deck.name === S.deck)) renderStart();
      else {
        const saved = pref("deck");
        pickDeck(decks.some((deck) => deck.name === saved) ? saved : (decks[0]?.name ?? null));
      }
    } else {
      const preserveCount = S.more > 0;
      S.more = decks.find((deck) => deck.name === S.deck)?.due ?? 0;
      renderResultsAvailability(preserveCount);
    }
  } catch {
    // Background failures preserve the current page; the next event or tick retries.
  } finally {
    if (S.dueRefresh === token) S.dueRefresh = null;
  }
}

async function startPass() {
  const epoch = accountEpoch;
  const input = $("start-count");
  const deck = S.decks.find((d) => d.name === S.deck);
  if (!deck || S.starting || !validCount(input.value, deck.due)) return;
  S.dueRefresh = null;
  const count = Number(input.value);
  setPref("batch", count);
  setPref("deck", deck.name);
  S.starting = true;
  S.message = "";
  renderStart();
  try {
    // "empty": the deck changed since the list was loaded, load it again
    const outcome = await post("api/start", { deck: deck.name, count });
    assertCurrent(epoch);
    if (outcome === "empty") {
      S.starting = false;
      return showStart();
    }
    location.reload();
  } catch (err) {
    if (interrupted(epoch, err)) return;
    S.starting = false;
    S.message = failure(err);
    renderStart();
  }
}

$("start-go").addEventListener("click", startPass);
$("start-count").addEventListener("input", (e) => {
  const deck = S.decks.find((d) => d.name === S.deck);
  $("start-go").disabled = !deck || S.starting || !validCount(e.target.value, deck.due);
});
$("start-count").addEventListener("keydown", (e) => {
  if (e.key !== "Enter" || e.isComposing) return;
  e.preventDefault();
  startPass();
});

// ---------- input: card ----------

function renderProgress() {
  $("progress").replaceChildren(...S.cards.map((c, i) => el("button", {
    type: "button",
    class: i === S.idx ? "current" : cardState(c.n),
    "aria-label": `Card ${i + 1}`,
    onclick: () => go(i),
  }, el("i"))));
  $("count").textContent = `${filled()} of ${S.cards.length}`;
}

function renderCard() {
  const card = S.cards[S.idx];
  renderProgress();
  $("pos").textContent = `${pad(S.idx + 1)} / ${pad(S.cards.length)}`;
  $("chip-new").hidden = !card.isNew;
  const q = $("q");
  q.replaceChildren(cardContent(card));
  q.classList.toggle("long", card.question.length > 180 || card.question.split("\n").length > 3);
  const ta = $("answer");
  ta.value = S.answers[card.n] ?? "";
  autosize(ta);
  $("prev").disabled = S.idx === 0;
  $("next").textContent = S.idx === S.cards.length - 1 ? "Check answers" : "Next →";
  ta.focus();
}

function commit() {
  S.answers[S.cards[S.idx].n] = $("answer").value;
}

function leave() {
  S.touched[S.cards[S.idx].n] = true;
}

function go(i) {
  commit();
  leave();
  S.idx = Math.max(0, Math.min(i, S.cards.length - 1));
  save();
  renderCard();
}

const isLast = () => S.idx === S.cards.length - 1;

function next() {
  if (!isLast()) return go(S.idx + 1);
  commit();
  leave();
  submit();
}

function mark(value) {
  $("answer").value = value;
  if (!isLast()) return go(S.idx + 1);
  commit();
  leave();
  save();
  renderCard();
  $("next").focus();
}

$("answer").addEventListener("input", (e) => {
  commit();
  autosize(e.target);
  save();
  renderProgress();
});

$("answer").addEventListener("keydown", (e) => {
  if (e.isComposing) return;
  if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
    e.preventDefault();
    commit();
    leave();
    submit();
  } else if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    next();
  } else if (e.key === "?" && e.target.value === "") {
    e.preventDefault();
    mark("?");
  }
});

$("prev").addEventListener("click", () => go(S.idx - 1));
$("next").addEventListener("click", next);
$("forgot").addEventListener("click", () => mark("?"));
$("skip").addEventListener("click", () => mark(""));
for (const b of $("tabs").querySelectorAll("button")) b.addEventListener("click", () => setView(b.dataset.view));

// ---------- input: list ----------

function numClass(n) {
  const state = cardState(n);
  return state === "answered" || state === "forgot" ? state : "";
}

function renderListBar() {
  const done = filled();
  $("list-count").textContent = `${done} of ${S.cards.length} answered`;
  $("list-mini").style.width = `${(done / S.cards.length) * 100}%`;
}

function renderList() {
  const fields = [];
  $("rows").replaceChildren(...S.cards.map((card, i) => {
    const num = el("span", { class: `num ${numClass(card.n)}` }, pad(i + 1));
    const ta = el("textarea", { rows: 1, placeholder: "answer · ? don't know · empty: skip", "aria-label": `Answer ${i + 1}`, spellcheck: "false" });
    ta.value = S.answers[card.n] ?? "";
    ta.addEventListener("input", () => {
      S.answers[card.n] = ta.value;
      num.className = `num ${numClass(card.n)}`;
      autosize(ta);
      save();
      renderListBar();
    });
    ta.addEventListener("focus", () => {
      S.idx = i;
      save();
    });
    ta.addEventListener("keydown", (e) => {
      if (e.isComposing || e.key !== "Enter" || e.shiftKey) return;
      e.preventDefault();
      if (e.metaKey || e.ctrlKey) return submit();
      (fields[i + 1] ?? $("list-submit")).focus();
    });
    fields.push(ta);
    return el("li", { class: "row" }, num, el("div", { class: "rq" }, cardContent(card)), ta);
  }));
  fields.forEach(autosize);
  renderListBar();
}

$("list-submit").addEventListener("click", () => submit());

// ---------- checking ----------

function renderChecking(note, bad = false) {
  const done = filled();
  const skipped = S.cards.length - done;
  const counts = [`${done} ${plural(done, "answer")}`];
  if (skipped) counts.push(`${skipped} skipped`);
  $("check-title").textContent = S.gradeError ? "Check failed" : `${S.provider === "codex" ? "Codex" : "Claude"} is checking your answers`;
  $("pulse").hidden = Boolean(S.gradeError);
  $("retry").hidden = !S.gradeError;
  const sub = $("check-sub");
  sub.textContent = note ?? `${counts.join(" · ")}. The page updates on its own…`;
  sub.classList.toggle("bad", bad);
}

function showGradeError(reason) {
  S.polling = null;
  S.gradeError = reason;
  show();
  renderChecking(reason, true);
}

async function submit() {
  const epoch = accountEpoch;
  S.screen = "checking";
  S.gradeError = "";
  save();
  show();
  renderChecking();
  try {
    const answers = S.cards.map((c) => ({ n: c.n, answer: norm(S.answers[c.n]) }));
    const outcome = await post("api/answers", { session: S.session, answers });
    assertCurrent(epoch);
    if (outcome === "session") return location.reload();
    pollResults();
  } catch (error) {
    if (interrupted(epoch, error)) return;
    renderChecking("Server not responding. Your answers are saved in this browser: reload the page later.", true);
  }
}

async function pollResults() {
  const epoch = accountEpoch;
  const token = (S.polling = Symbol("results"));
  let fails = 0;
  while (currentAccount(epoch) && S.polling === token) {
    try {
      const res = await call("api/results", { cache: "no-store" });
      if (res.status === 200) {
        const data = await res.json();
        assertCurrent(epoch);
        if (S.polling !== token) return;
        return showResults(data);
      }
      if (res.status === 409) {
        const reply = await res.json().catch(emptyJSON);
        assertCurrent(epoch);
        if (S.polling !== token) return;
        if (reply.code === "grading_failed") return showGradeError(reply.error);
      }
      fails = 0;
    } catch (error) {
      if (interrupted(epoch, error)) return;
      if (++fails === 3) renderChecking("Server not responding, waiting…", true);
    }
    await sleep(POLL_MS);
  }
}

async function regrade() {
  const epoch = accountEpoch;
  $("retry").disabled = true;
  try {
    const outcome = await post("api/regrade", { session: S.session });
    assertCurrent(epoch);
    if (outcome === "session") return location.reload();
    S.gradeError = "";
    show();
    renderChecking();
    pollResults();
  } catch (err) {
    if (interrupted(epoch, err)) return;
    renderChecking(failure(err), true);
  } finally {
    if (currentAccount(epoch)) $("retry").disabled = false;
  }
}

$("retry").addEventListener("click", regrade);

// ---------- results ----------

function showResults(data) {
  S.polling = null;
  S.results = data.items;
  S.screen = "results";
  for (const item of S.results) {
    if (item.skipped) continue;
    // the confirmed grade is final; an unsent change of mine beats the suggested grade
    S.grades[item.n] = data.confirmed?.[item.n] ?? (GRADES.includes(S.grades[item.n]) ? S.grades[item.n] : item.grade);
  }
  if (S.open === null) S.open = Object.fromEntries(S.results.filter((i) => i.grade === "Again" || i.grade === "Hard").map((i) => [i.n, true]));
  S.locked = Boolean(data.confirmed);
  S.written = Boolean(data.status?.written);
  S.message = data.status?.message ?? "";
  S.more = data.status?.more ?? 0;
  save();
  show();
  renderResults();
  if (S.locked && !S.written) pollStatus();
}

function resultRow(item) {
  const num = el("span", { class: "num" }, pad(item.n));
  const question = el("span", { class: "rq" }, ...rich(item.question));
  if (item.skipped) {
    return el("li", { class: "res skipped" },
      el("div", { class: "res-head" }, num, question, el("span", { class: "ivl" }, "still due"), el("span", { class: "badge skip" }, "skipped")),
      item.question_html && cardContent(item));
  }
  const grade = S.grades[item.n];
  const open = Boolean(S.open[item.n]);
  const interval = item.intervals?.[grade];
  const head = el("button", {
    type: "button",
    class: "res-head",
    "aria-expanded": String(open),
    onclick: () => {
      S.open[item.n] = !open;
      renderResults();
    },
  }, num, question, el("span", { class: "ivl" }, interval ? `→ ${interval}` : ""), el("span", { class: `badge ${grade.toLowerCase()}` }, grade));
  const facts = el("dl", { class: "kv" },
    item.question_html && [el("dt", {}, "Question"), el("dd", {}, cardContent(item))],
    el("dt", {}, "You"), el("dd", { class: "you" }, item.answer === "?" ? "don't know" : item.answer),
    item.mistake && [el("dt", {}, "Mistake"), el("dd", { class: "err" }, ...rich(item.mistake))],
    (item.back || item.back_html) && [el("dt", {}, "Key"), el("dd", {}, cardContent(item, "back"))],
    item.note && [el("dt", {}, "Note"), el("dd", { class: "note" }, ...rich(item.note))]);
  const buttons = el("div", { class: "grades" }, ...GRADES.map((g) => el("button", {
    type: "button",
    class: `g ${g.toLowerCase()}${g === grade ? " sel" : ""}`,
    "aria-pressed": String(g === grade),
    disabled: S.locked,
    onclick: () => {
      S.grades[item.n] = g;
      save();
      renderResults();
    },
  }, el("b", {}, g), el("span", {}, item.intervals?.[g] ?? ""))));
  return el("li", { class: `res${open ? " open" : ""}` }, head, el("div", { class: "res-body" }, facts, buttons));
}

function renderResults() {
  const graded = S.results.filter((i) => !i.skipped);
  const count = Object.fromEntries(GRADES.map((g) => [g, 0]));
  for (const item of graded) count[S.grades[item.n]] += 1;
  $("res-label").textContent = `Checked ${graded.length} of ${S.results.length}`;
  $("counters").replaceChildren(...GRADES.map((g) => el("div", { class: `counter ${g.toLowerCase()}` }, el("b", {}, String(count[g])), el("span", {}, g))));
  $("strip").replaceChildren(...S.results.map((i) => el("i", { class: i.skipped ? "" : S.grades[i.n].toLowerCase() })));
  const weak = [...new Set(graded
    .filter((i) => S.grades[i.n] === "Again" || S.grades[i.n] === "Hard")
    .map((i) => i.topic || i.question.split("\n")[0].slice(0, 48)))];
  $("weak").hidden = weak.length === 0;
  $("chips").replaceChildren(...weak.map((t) => el("span", { class: "chip" }, t)));
  $("res-rows").replaceChildren(...S.results.map(resultRow));
  renderResultsAvailability();
}

function renderResultsAvailability(preserveCount = false) {
  const hint = $("res-hint");
  const more = S.written && S.more > 0;
  hint.classList.toggle("bad", Boolean(S.message));
  $("done").hidden = !S.written;
  $("write").hidden = S.written;
  $("write").disabled = S.locked;
  $("write").textContent = S.locked ? "Saving…" : "Save to Anki";
  $("res-wrap").classList.toggle("has-more", more);
  $("take").hidden = !more;
  $("to-decks").hidden = !S.written || more;
  $("back").hidden = !canLeave();
  const input = $("take-count");
  input.max = String(S.more);
  input.disabled = S.taking;
  // never overwrite what is being typed
  if (more && !preserveCount && document.activeElement !== input) input.value = String(batchSize(S.more));
  $("more").disabled = S.taking || !validCount(input.value, S.more);
  $("more").textContent = S.taking ? "Taking cards…" : "More cards";
  if (S.message) hint.textContent = S.message;
  else if (more) hint.textContent = `${S.more} more ${plural(S.more, "card")} in this deck, how many to take?`;
  else if (S.written) hint.textContent = "That's all for this deck today.";
  else hint.textContent = S.locked ? "Saving grades to Anki." : "Disagree with a grade? Change it, yours wins.";
}

async function takeMore() {
  const epoch = accountEpoch;
  const input = $("take-count");
  if (S.taking || !validCount(input.value, S.more)) return;
  S.dueRefresh = null;
  const count = Number(input.value);
  setPref("batch", count);
  S.taking = true;
  S.message = "";
  renderResults();
  try {
    const outcome = await post("api/more", { session: S.session, count });
    assertCurrent(epoch);
    if (outcome === "empty") {
      S.taking = false;
      S.more = 0;
      return renderResults();
    }
    // the server switched to the next pass (this tab or another one asked): start over from its state
    location.reload();
  } catch (err) {
    if (interrupted(epoch, err)) return;
    S.taking = false;
    S.message = failure(err);
    renderResults();
  }
}

$("more").addEventListener("click", takeMore);
$("take-count").addEventListener("input", (e) => {
  $("more").disabled = S.taking || !validCount(e.target.value, S.more);
});
$("take-count").addEventListener("keydown", (e) => {
  if (e.key !== "Enter" || e.isComposing) return;
  e.preventDefault();
  takeMore();
});
$("take-count").addEventListener("blur", (e) => {
  if (!validCount(e.target.value, S.more)) e.target.value = String(clampCount(Number(e.target.value), S.more));
  $("more").disabled = S.taking || !validCount(e.target.value, S.more);
});

async function write() {
  const epoch = accountEpoch;
  S.locked = true;
  S.message = "";
  renderResults();
  try {
    const grades = S.results.filter((i) => !i.skipped).map((i) => ({ n: i.n, grade: S.grades[i.n] }));
    const sent = await post("api/confirm", { session: S.session, grades });
    assertCurrent(epoch);
    if (sent === "session") return location.reload();
    // another tab confirmed first: show the grades the server holds, not this tab's
    if (sent === "sent") {
      const data = await requestJSON("api/results", { cache: "no-store" });
      assertCurrent(epoch);
      return showResults(data);
    }
    pollStatus();
  } catch (error) {
    if (interrupted(epoch, error)) return;
    S.locked = false;
    S.message = "Server not responding, grades not sent.";
    renderResults();
  }
}

async function pollStatus() {
  const epoch = accountEpoch;
  const token = (S.polling = Symbol("status"));
  while (currentAccount(epoch) && S.polling === token) {
    try {
      const res = await call("api/status", { cache: "no-store" });
      if (res.status === 200) {
        const status = await res.json();
        assertCurrent(epoch);
        if (S.polling !== token) return;
        S.message = status.message ?? "";
        S.more = status.more ?? 0;
        S.written = Boolean(status.written);
        // nothing was written: the grades unlock and Save to Anki can be pressed again
        if (!S.written && status.retry) S.locked = false;
        renderResults();
        if (S.written || !S.locked) {
          S.polling = null;
          return save();
        }
      }
    } catch (error) { if (interrupted(epoch, error)) return; }
    await sleep(POLL_MS);
  }
}

$("write").addEventListener("click", write);

// ---------- setup, login, settings ----------

const SETUP_FIELDS = { grader_provider: "setup-provider", codex_auth: "setup-codex", user: "setup-user", password: "setup-password", claude_token: "setup-claude", anki_key: "setup-anki" };
const SETTINGS_FIELDS = { grader_provider: "set-provider", codex_auth: "set-codex", password: "set-current", new_password: "set-new", claude_token: "set-claude", anki_key: "set-anki" };

// the same help text in both forms: each slot takes its template once
for (const slot of document.querySelectorAll("[data-help]")) slot.replaceWith($(`tpl-${slot.dataset.help}`).content.cloneNode(true));

for (const button of document.querySelectorAll("[data-reveal]")) {
  button.addEventListener("click", () => {
    const input = $(button.dataset.reveal);
    const reveal = input.type === "password";
    input.type = reveal ? "text" : "password";
    button.textContent = reveal ? "Hide" : "Show";
    button.setAttribute("aria-pressed", String(reveal));
  });
}

function providerFields(prefix) {
  const codex = $(`${prefix}-provider`).value === "codex";
  $(`${prefix}-codex-field`).hidden = !codex;
  $(`${prefix}-claude-field`).hidden = codex;
}
for (const prefix of ["setup", "set"]) $(`${prefix}-provider`).addEventListener("change", () => providerFields(prefix));

async function authFile(id, required = false) {
  const epoch = accountEpoch;
  const file = $(id).files[0];
  if (!file) {
    if (required) throw { field: "codex_auth", message: "Choose auth.json from a dedicated Codex ChatGPT login." };
    return undefined;
  }
  if (file.size > 1024 * 1024) throw { field: "codex_auth", message: "auth.json is too large." };
  try {
    const text = await file.text();
    assertCurrent(epoch);
    JSON.parse(text);
    return text;
  } catch (error) { assertCurrent(epoch); throw { field: "codex_auth", message: "Choose a valid auth.json file." }; }
}

function savedProviderStatus() {
  $("grader-status").textContent = `${S.provider === "codex" ? "Codex" : "Claude Code"} selected. ${S.graderConnected ? "Credentials saved; checked when selected or replaced. Availability is checked on the next grading call." : "No saved credentials available."}`;
}

// the checks mirror the server's rules; the server stays the judge
const utf8Length = (text) => new TextEncoder().encode(text).length;
const runeCount = (text) => [...text].length;

function loginError(user) {
  if (!user) return "Enter a login.";
  if (runeCount(user) > 64) return "At most 64 characters.";
  if (/\p{Cc}/u.test(user)) return "No control characters.";
  return "";
}

function passwordError(password) {
  if (runeCount(password) < 12) return "At least 12 characters.";
  if (runeCount(password) > 1024) return "At most 1024 characters.";
  return "";
}

function claudeError(token) {
  if (!token) return "";
  if (!token.startsWith("sk-ant-")) return "Must start with sk-ant-.";
  if (utf8Length(token) > 4096) return "Too long.";
  if (/\s/.test(token)) return "No spaces inside the token.";
  return "";
}

function ankiError(key) {
  if (!key) return "";
  if (utf8Length(key) > 1024) return "Too long.";
  if (/\s/.test(key)) return "No spaces inside the key.";
  return "";
}

function say(id, text = "", bad = false) {
  const node = $(id);
  node.textContent = text;
  node.classList.toggle("bad", bad);
  node.hidden = !text;
}

function setFieldError(id, message = "") {
  const node = $(`${id}-error`);
  node.textContent = message;
  node.hidden = !message;
  $(id).setAttribute("aria-invalid", String(Boolean(message)));
}

// applies every message and returns the first invalid field's id ("" when all pass)
function markErrors(errors) {
  let first = "";
  for (const [id, message] of Object.entries(errors)) {
    setFieldError(id, message);
    if (message && !first) first = id;
  }
  return first;
}

function clearForm(form) {
  for (const input of form.querySelectorAll("input, select")) if ($(`${input.id}-error`)) setFieldError(input.id, "");
  for (const button of form.querySelectorAll("[data-reveal]")) {
    $(button.dataset.reveal).type = "password";
    button.textContent = "Show";
    button.setAttribute("aria-pressed", "false");
  }
  form.reset();
}

function clearAccountDOM() {
  for (const id of ["setup-form", "login-form", "settings-form"]) clearForm($(id));
  for (const id of ["answer", "knowledge-decks", "invite-link", "start-import-file", "set-import-file"]) $(id).value = "";
  for (const id of ["q", "rows", "res-rows", "decks", "counters", "chips", "strip", "progress"]) $(id).replaceChildren();
  $("invite-link-field").hidden = true;
  for (const id of ["setup-status", "login-status", "set-status", "invite-status", "preferences-status", "start-import-status", "set-import-status"]) say(id);
  $("grader-status").textContent = "";
  $("meta").textContent = "";
  for (const id of ["setup-submit", "login-submit", "set-submit", "invite-create", "preferences-save"]) $(id).disabled = false;
}

const collectionAvailable = () => S.screen === "start" || (S.screen === "results" && S.written);

function configureAccountUI() {
  $("setup-anki-field").hidden = Boolean(invitation);
  $("set-anki-field").hidden = S.managed;
  $("invitations").hidden = !S.authed || !S.multiAccount || !S.isOwner;
  $("registration-login").hidden = !invitation;
  for (const hint of document.querySelectorAll(".legacy-login-help")) hint.hidden = S.multiAccount || Boolean(invitation);
  for (const prefix of ["start-import", "set-import"]) {
    $(prefix).hidden = !S.authed;
    $(`${prefix}-submit`).disabled = S.importing || !collectionAvailable();
    $(`${prefix}-file`).disabled = S.importing || !collectionAvailable();
  }
  $("preferences-save").disabled = !collectionAvailable();
  $("knowledge-decks").disabled = !collectionAvailable();
}

function showSetup() {
  const provider = S.provider;
  resetAccount();
  S.provider = provider;
  S.screen = "setup";
  $("meta").textContent = "";
  $("setup-title").textContent = invitation ? "Create your account" : "Set up";
  $("setup-lede").textContent = invitation
    ? "Choose your login and password, then add your own grading credentials. After registering, import your Anki package."
    : "Create the owner login. Choose a grading provider. The server checks the credentials, then encrypts them under this password.";
  $("setup-submit").textContent = invitation ? "Create account" : "Set up";
  show();
  $("setup-provider").value = S.provider;
  providerFields("setup");
  $("setup-user").focus();
}

function showLogin() {
  resetAccount();
  S.screen = "login";
  $("meta").textContent = "";
  clearForm($("login-form"));
  say("login-status");
  show();
  $("login-user").focus();
}

function clearInvitation() {
  invitation = "";
  const url = new URL(location.href);
  url.hash = "";
  history.replaceState(null, "", url.href);
}

async function submitSetup(event) {
  event.preventDefault();
  if (S.authBusy) return;
  const epoch = accountEpoch;
  const invited = invitation;
  const user = $("setup-user").value.trim();
  const password = $("setup-password").value;
  const claude = $("setup-claude").value.trim();
  const anki = $("setup-anki").value.trim();
  const provider = $("setup-provider").value;
  const first = markErrors({
    "setup-user": loginError(user),
    "setup-password": passwordError(password),
    "setup-repeat": password === $("setup-repeat").value ? "" : "The passwords don't match.",
    "setup-claude": provider !== "claude" ? "" : !claude && (invited || S.multiAccount) ? "Enter your own Claude token." : claudeError(claude),
    "setup-anki": invited ? "" : ankiError(anki),
  });
  if (first) return $(first).focus();
  const submit = $("setup-submit");
  S.authBusy = true;
  submit.disabled = true;
  submit.textContent = "Checking…";
  say("setup-status", `Checking your ${provider === "codex" ? "Codex" : "Claude"} credentials and collection…`);
  try {
    const codexAuth = provider === "codex" ? await authFile("setup-codex", true) : undefined;
    assertCurrent(epoch);
    const body = { user, password, grader_provider: provider, codex_auth: codexAuth, claude_token: provider === "claude" ? claude : "" };
    if (invited) body.invitation = invited;
    else body.anki_key = anki;
    const outcome = await post(invited ? "api/register" : "api/setup", body);
    assertCurrent(epoch);
    if (outcome !== "ok") throw new Error("Account registration is busy. Try again shortly.");
    clearForm($("setup-form"));
    if (invited) clearInvitation();
    return boot();
  } catch (err) {
    if (interrupted(epoch, err)) return;
    say("setup-status");
    if (err.code === "setup_done") return boot();
    const id = SETUP_FIELDS[err.field];
    if (id) { setFieldError(id, err.message); return $(id).focus(); }
    say("setup-status", failure(err), true);
  } finally {
    if (currentAccount(epoch)) { S.authBusy = false; submit.disabled = false; submit.textContent = invited ? "Create account" : "Set up"; }
  }
}

async function submitLogin(event) {
  event.preventDefault();
  if (S.authBusy) return;
  const epoch = accountEpoch;
  const user = $("login-user").value.trim();
  const password = $("login-password").value;
  if (!user || !password) return say("login-status", "Enter your login and password.", true);
  S.authBusy = true;
  $("login-submit").disabled = true;
  say("login-status");
  try {
    await post("api/login", { user, password });
    assertCurrent(epoch);
    clearForm($("login-form"));
    return boot();
  } catch (err) {
    if (interrupted(epoch, err)) return;
    if (err.code === "setup") return boot();
    say("login-status", err.status === 401 ? "Wrong login or password" : failure(err), true);
    $("login-password").value = "";
    $("login-password").focus();
  } finally {
    if (currentAccount(epoch)) { S.authBusy = false; $("login-submit").disabled = false; }
  }
}

async function logout() {
  const expected = S.accountId;
  showLogin();
  const epoch = accountEpoch;
  S.authBusy = true;
  $("login-submit").disabled = true;
  let complete = false;
  try { await post("api/logout", {}, expected); assertCurrent(epoch); complete = true; }
  catch (error) { if (interrupted(epoch, error)) return; say("login-status", "Could not finish logging out. Reload before logging in again.", true); }
  finally { if (currentAccount(epoch)) { S.authBusy = !complete; $("login-submit").disabled = !complete; } }
}

function openSettings() {
  S.settings = true;
  clearForm($("settings-form"));
  $("set-provider").value = S.provider;
  providerFields("set");
  savedProviderStatus();
  say("set-status");
  say("preferences-status", collectionAvailable() ? "Loading your deck preferences…" : "Finish or drop the current batch before changing deck preferences or importing cards.");
  show();
  loadPreferences();
  $("set-current").focus();
}

function closeSettings() {
  S.settings = false;
  clearForm($("settings-form"));
  show();
}

async function saveSettings(event) {
  event.preventDefault();
  const epoch = accountEpoch;
  const current = $("set-current").value;
  const next = $("set-new").value;
  const repeat = $("set-repeat").value;
  const claude = $("set-claude").value.trim();
  const anki = $("set-anki").value.trim();
  const provider = $("set-provider").value;
  const changing = next !== "" || repeat !== "";
  const needsClaude = provider === "claude" && S.multiAccount && provider === S.provider && !S.graderConnected;
  const first = markErrors({
    "set-current": current ? "" : "Enter your current password.",
    "set-new": !changing ? "" : next === "" ? "Enter the new password too." : passwordError(next),
    "set-repeat": next !== "" && next !== repeat ? "The passwords don't match." : "",
    "set-claude": provider !== "claude" ? "" : needsClaude && !claude ? "Enter your own Claude token." : claudeError(claude),
    "set-anki": S.managed ? "" : ankiError(anki),
  });
  if (first) return $(first).focus();
  const body = { password: current };
  if (next) body.new_password = next;
  if (claude && provider === "claude") body.claude_token = claude;
  if (provider !== S.provider) body.grader_provider = provider;
  if (anki && !S.managed) body.anki_key = anki;
  const submit = $("set-submit");
  submit.disabled = true;
  say("set-status", "Checking changes and saving…");
  try {
    if (provider === "codex") body.codex_auth = await authFile("set-codex", provider === S.provider && !S.graderConnected);
    assertCurrent(epoch);
    await post("api/settings", body);
    assertCurrent(epoch);
    clearForm($("settings-form"));
    S.provider = provider;
    S.graderConnected = true;
    $("set-provider").value = provider;
    providerFields("set");
    savedProviderStatus();
    say("set-status", "Saved. The selected provider will be used on the next grading call.");
  } catch (err) {
    if (interrupted(epoch, err)) return;
    if (err.code === "credentials") { setFieldError("set-current", "Wrong current password."); return $("set-current").focus(); }
    const id = SETTINGS_FIELDS[err.field];
    if (id) { setFieldError(id, err.message); return $(id).focus(); }
    say("set-status", failure(err), true);
  } finally { if (currentAccount(epoch)) submit.disabled = false; }
}

async function loadPreferences() {
  const epoch = accountEpoch;
  try {
    const data = await requestJSON("api/preferences", { cache: "no-store" });
    assertCurrent(epoch);
    S.knowledgeDecks = Array.isArray(data.knowledge_decks) ? data.knowledge_decks : [];
    $("knowledge-decks").value = S.knowledgeDecks.join("\n");
    if (collectionAvailable()) say("preferences-status");
  } catch (error) { if (!interrupted(epoch, error)) say("preferences-status", failure(error), true); }
}

async function savePreferences(event) {
  event.preventDefault();
  const epoch = accountEpoch;
  const decks = $("knowledge-decks").value.split("\n").map((name) => name.trim()).filter(Boolean);
  if (decks.length > 64 || decks.some((name) => utf8Length(name) > 256)) return say("preferences-status", "Use at most 64 deck names, each at most 256 bytes.", true);
  $("preferences-save").disabled = true;
  try {
    const outcome = await post("api/preferences", { knowledge_decks: decks });
    assertCurrent(epoch);
    if (outcome !== "ok") return say("preferences-status", "Finish or drop the current batch before changing deck preferences.", true);
    S.knowledgeDecks = decks;
    say("preferences-status", "Deck preferences saved for your next batch.");
  } catch (error) { if (!interrupted(epoch, error)) say("preferences-status", failure(error), true); }
  finally { if (currentAccount(epoch)) configureAccountUI(); }
}

async function importCards(prefix) {
  const epoch = accountEpoch;
  const file = $(`${prefix}-file`).files[0];
  if (!file) return say(`${prefix}-status`, "Choose an .apkg package.", true);
  if (file.size > 8 * 1024 * 1024) return say(`${prefix}-status`, "The package must be at most 8 MiB. Export with Support older Anki versions enabled.", true);
  if (!file.name.toLowerCase().endsWith(".apkg")) return say(`${prefix}-status`, "Choose an .apkg package. Export with Support older Anki versions enabled.", true);
  if (!collectionAvailable() || S.importing) return say(`${prefix}-status`, "Finish or drop the current batch before importing cards.", true);
  S.dueRefresh = null;
  S.importing = true;
  configureAccountUI();
  say(`${prefix}-status`, "Importing cards, media and scheduling…");
  try {
    await requestJSON("api/import", { method: "POST", headers: { "Content-Type": "application/octet-stream", "X-Anki-Import": "1" }, body: file });
    assertCurrent(epoch);
    $(`${prefix}-file`).value = "";
    say(`${prefix}-status`, "Cards imported. They will appear in your deck list.");
    if (S.screen === "start") await showStart();
  } catch (error) {
    if (interrupted(epoch, error)) return;
    say(`${prefix}-status`, error.code === "busy" ? "Finish or drop the current batch before importing cards." : failure(error), true);
  } finally { if (currentAccount(epoch)) { S.importing = false; configureAccountUI(); } }
}

async function createInvitation() {
  const epoch = accountEpoch;
  if (!S.isOwner || !S.multiAccount) return;
  $("invite-create").disabled = true;
  try {
    const data = await requestJSON("api/invitations", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
    assertCurrent(epoch);
    if (typeof data.token !== "string" || !data.token) throw new Error("Invitation response was incomplete.");
    S.inviteLink = invitationLink(data.token);
    $("invite-link").value = S.inviteLink;
    $("invite-link-field").hidden = false;
    say("invite-status", `Invitation created. Expires ${new Date(data.expires_at).toLocaleString()}.`);
  } catch (error) { if (!interrupted(epoch, error)) say("invite-status", failure(error), true); }
  finally { if (currentAccount(epoch)) $("invite-create").disabled = false; }
}

async function copyInvitation() {
  const epoch = accountEpoch;
  try { await navigator.clipboard.writeText(S.inviteLink); assertCurrent(epoch); say("invite-status", "Invitation link copied."); }
  catch (error) { if (!interrupted(epoch, error)) say("invite-status", "Copy the invitation link from the field above.", true); }
}

$("setup-form").addEventListener("submit", submitSetup);
$("login-form").addEventListener("submit", submitLogin);
$("settings-form").addEventListener("submit", saveSettings);
$("settings-btn").addEventListener("click", openSettings);
$("set-back").addEventListener("click", closeSettings);
$("logout").addEventListener("click", logout);
$("preferences-form").addEventListener("submit", savePreferences);
$("invite-create").addEventListener("click", createInvitation);
$("invite-copy").addEventListener("click", copyInvitation);
$("registration-login").addEventListener("click", () => { clearInvitation(); showLogin(); });
for (const prefix of ["start-import", "set-import"]) $(`${prefix}-submit`).addEventListener("click", () => importCards(prefix));

// ---------- boot ----------

function showError() {
  S.screen = "error";
  show();
}

async function boot() {
  let epoch = accountEpoch;
  let info;
  try {
    info = await requestJSON("api/session", { cache: "no-store" });
    assertCurrent(epoch);
  } catch (error) {
    if (!interrupted(epoch, error)) showError();
    return;
  }
  S.multiAccount = Boolean(info.multi_account);
  S.provider = info.grader_provider || "claude";
  if (invitation && S.multiAccount) return showSetup();
  if (info.state === "setup") return showSetup();
  if (info.state === "login") return showLogin();
  if (info.state !== "ready") return showError();
  selectAccount(info);
  epoch = accountEpoch;
  S.authed = true;
  let data;
  try {
    data = await requestJSON("api/cards", { cache: "no-store" });
    assertCurrent(epoch);
  } catch (error) { if (!interrupted(epoch, error)) showError(); return; }
  if (data.stage === "idle") return showStart();
  S.session = data.session;
  S.deck = data.deck;
  S.cards = data.cards;
  const saved = load(data.session);
  if (saved) {
    S.answers = saved.answers ?? {};
    S.touched = saved.touched ?? {};
    S.idx = Math.min(Math.max(saved.idx ?? 0, 0), S.cards.length - 1);
    S.view = saved.view === "list" ? "list" : "card";
    S.grades = saved.grades ?? {};
  }
  for (const answer of data.answers ?? []) S.answers[answer.n] = answer.answer;
  $("meta").textContent = [data.deck, `${data.due} due`, data.fresh ? `${data.fresh} new` : null].filter(Boolean).join(" · ");
  if (data.stage === "input") { S.screen = "input"; return show(); }
  if (data.stage === "checking") {
    S.screen = "checking";
    if (data.gradeError) return showGradeError(data.gradeError);
    show(); renderChecking(); return pollResults();
  }
  try {
    const results = await requestJSON("api/results", { cache: "no-store" });
    assertCurrent(epoch);
    showResults(results);
  } catch (error) { if (!interrupted(epoch, error)) showError(); }
}

setInterval(refreshDueCards, DUE_REFRESH_MS);
document.addEventListener("visibilitychange", refreshDueCards);
for (const event of ["focus", "pageshow", "online"]) window.addEventListener(event, refreshDueCards);

boot();

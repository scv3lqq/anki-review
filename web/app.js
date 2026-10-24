"use strict";
const GRADES = ["Again", "Hard", "Good", "Easy"];
const POLL_MS = 1500;
const STORE = "anki-review";
const DEFAULT_BATCH = 25;
const FIELD_SIZING = CSS.supports("field-sizing", "content");
const $ = (id) => document.getElementById(id);
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const pad = (n) => String(n).padStart(2, "0");

const S = {
  session: null, cards: [], screen: "boot", view: "card", idx: 0,
  answers: {}, touched: {}, grades: {}, open: null,
  results: null, locked: false, written: false, message: "", polling: null, more: 0, taking: false,
  decks: [], deck: null, starting: false, gradeError: "",
};

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
  if (!S.session) return;
  const data = { answers: S.answers, touched: S.touched, idx: S.idx, view: S.view, grades: S.grades };
  try {
    localStorage.setItem(`${STORE}:${S.session}`, JSON.stringify(data));
  } catch {}
}

function load(session) {
  try {
    return JSON.parse(localStorage.getItem(`${STORE}:${session}`) || "null");
  } catch {
    return null;
  }
}

function forget(session) {
  try {
    localStorage.removeItem(`${STORE}:${session}`);
  } catch {}
}

function pref(name) {
  try {
    return localStorage.getItem(`${STORE}:${name}`);
  } catch {
    return null;
  }
}

function setPref(name, value) {
  try {
    localStorage.setItem(`${STORE}:${name}`, String(value));
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

// "ok", or the conflict code the server sent: "sent" (this stage is already posted), "session"
// (this tab shows an older pass), "empty" (no cards left), "busy" (a stage is still running)
async function post(path, body) {
  const res = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  if (res.ok) return "ok";
  const reply = await res.json().catch(() => ({}));
  if (res.status === 409 && ["sent", "session", "empty", "busy"].includes(reply.code)) return reply.code;
  throw new Error(reply.error ?? `HTTP ${res.status}`);
}

// ---------- screens ----------

function canLeave() {
  if (S.screen === "input" || S.screen === "results") return !S.locked || S.written;
  return S.screen === "checking" && Boolean(S.gradeError);
}

function show() {
  const input = S.screen === "input";
  $("tabs").hidden = !input;
  for (const b of $("tabs").querySelectorAll("button")) b.setAttribute("aria-pressed", String(b.dataset.view === S.view));
  $("back").hidden = !canLeave();
  $("start").hidden = S.screen !== "start";
  $("start-bar").hidden = S.screen !== "start";
  $("card-view").hidden = !(input && S.view === "card");
  $("list-view").hidden = !(input && S.view === "list");
  $("list-bar").hidden = !(input && S.view === "list");
  $("checking").hidden = S.screen !== "checking";
  $("results").hidden = S.screen !== "results";
  $("res-bar").hidden = S.screen !== "results";
  $("error").hidden = S.screen !== "error";
  if (input && S.view === "card") renderCard();
  if (input && S.view === "list") renderList();
}

function setView(view) {
  if (S.screen !== "input" || view === S.view) return;
  if (S.view === "card") commit();
  S.view = view;
  save();
  show();
}

async function backToDecks() {
  if (S.screen === "input" && filled() > 0 && !confirm("Drop this batch? Your typed answers will be lost.")) return;
  if (S.screen === "results" && !S.written && !confirm("Grades aren't saved to Anki yet. Drop this batch?")) return;
  try {
    // any outcome ends in a reload: it shows whatever the server holds now
    if ((await post("api/reset", { session: S.session })) === "ok") forget(S.session);
  } catch {}
  location.reload();
}

$("back").addEventListener("click", backToDecks);
$("to-decks").addEventListener("click", backToDecks);

// ---------- start: deck list ----------

async function showStart() {
  S.screen = "start";
  S.session = null;
  S.message = "";
  $("meta").textContent = "";
  show();
  $("start-hint").textContent = "Loading decks…";
  $("start-go").disabled = true;
  try {
    const res = await fetch("api/decks", { cache: "no-store" });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error ?? `HTTP ${res.status}`);
    S.decks = data.decks ?? [];
  } catch (err) {
    S.decks = [];
    S.message = failure(err);
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

async function startPass() {
  const input = $("start-count");
  const deck = S.decks.find((d) => d.name === S.deck);
  if (!deck || S.starting || !validCount(input.value, deck.due)) return;
  const count = Number(input.value);
  setPref("batch", count);
  setPref("deck", deck.name);
  S.starting = true;
  S.message = "";
  renderStart();
  try {
    // "empty": the deck changed since the list was loaded, load it again
    if ((await post("api/start", { deck: deck.name, count })) === "empty") {
      S.starting = false;
      return showStart();
    }
    location.reload();
  } catch (err) {
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
  q.replaceChildren(...rich(card.question));
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
    return el("li", { class: "row" }, num, el("div", { class: "rq" }, ...rich(card.question)), ta);
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
  $("check-title").textContent = S.gradeError ? "Check failed" : "Claude is checking your answers";
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
  S.screen = "checking";
  S.gradeError = "";
  save();
  show();
  renderChecking();
  try {
    const answers = S.cards.map((c) => ({ n: c.n, answer: norm(S.answers[c.n]) }));
    if ((await post("api/answers", { session: S.session, answers })) === "session") return location.reload();
    pollResults();
  } catch {
    renderChecking("Server not responding. Your answers are saved in this browser: reload the page later.", true);
  }
}

async function pollResults() {
  const token = (S.polling = Symbol("results"));
  let fails = 0;
  while (S.polling === token) {
    try {
      const res = await fetch("api/results", { cache: "no-store" });
      if (res.status === 200) return showResults(await res.json());
      if (res.status === 409) {
        const reply = await res.json().catch(() => ({}));
        if (reply.code === "grading_failed") return showGradeError(reply.error);
      }
      fails = 0;
    } catch {
      if (++fails === 3) renderChecking("Server not responding, waiting…", true);
    }
    await sleep(POLL_MS);
  }
}

async function regrade() {
  $("retry").disabled = true;
  try {
    if ((await post("api/regrade", { session: S.session })) === "session") return location.reload();
    S.gradeError = "";
    show();
    renderChecking();
    pollResults();
  } catch (err) {
    renderChecking(failure(err), true);
  } finally {
    $("retry").disabled = false;
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
    // the confirmed grade is final; an unsent change of mine beats Claude's grade
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
      el("div", { class: "res-head" }, num, question, el("span", { class: "ivl" }, "still due"), el("span", { class: "badge skip" }, "skipped")));
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
    el("dt", {}, "You"), el("dd", { class: "you" }, item.answer === "?" ? "don't know" : item.answer),
    item.mistake && [el("dt", {}, "Mistake"), el("dd", { class: "err" }, ...rich(item.mistake))],
    item.ref && [el("dt", {}, "Key"), el("dd", {}, ...rich(item.ref))],
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
  if (more && document.activeElement !== input) input.value = String(batchSize(S.more));
  $("more").disabled = S.taking || !validCount(input.value, S.more);
  $("more").textContent = S.taking ? "Taking cards…" : "More cards";
  if (S.message) hint.textContent = S.message;
  else if (more) hint.textContent = `${S.more} more ${plural(S.more, "card")} in this deck, how many to take?`;
  else if (S.written) hint.textContent = "That's all for this deck today.";
  else hint.textContent = S.locked ? "Saving grades to Anki." : "Disagree with a grade? Change it, yours wins.";
}

async function takeMore() {
  const input = $("take-count");
  if (S.taking || !validCount(input.value, S.more)) return;
  const count = Number(input.value);
  setPref("batch", count);
  S.taking = true;
  S.message = "";
  renderResults();
  try {
    if ((await post("api/more", { session: S.session, count })) === "empty") {
      S.taking = false;
      S.more = 0;
      return renderResults();
    }
    // the server switched to the next pass (this tab or another one asked): start over from its state
    location.reload();
  } catch (err) {
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
  S.locked = true;
  S.message = "";
  renderResults();
  try {
    const grades = S.results.filter((i) => !i.skipped).map((i) => ({ n: i.n, grade: S.grades[i.n] }));
    const sent = await post("api/confirm", { session: S.session, grades });
    if (sent === "session") return location.reload();
    // another tab confirmed first: show the grades the server holds, not this tab's
    if (sent === "sent") return showResults(await (await fetch("api/results", { cache: "no-store" })).json());
    pollStatus();
  } catch {
    S.locked = false;
    S.message = "Server not responding, grades not sent.";
    renderResults();
  }
}

async function pollStatus() {
  const token = (S.polling = Symbol("status"));
  while (S.polling === token) {
    try {
      const res = await fetch("api/status", { cache: "no-store" });
      if (res.status === 200) {
        const status = await res.json();
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
    } catch {}
    await sleep(POLL_MS);
  }
}

$("write").addEventListener("click", write);

// ---------- boot ----------

function showError() {
  S.screen = "error";
  show();
}

async function boot() {
  let data;
  try {
    const res = await fetch("api/cards", { cache: "no-store" });
    if (!res.ok) throw new Error(String(res.status));
    data = await res.json();
  } catch {
    return showError();
  }
  if (data.stage === "idle") return showStart();
  S.session = data.session;
  S.cards = data.cards;
  const saved = load(data.session);
  if (saved) {
    S.answers = saved.answers ?? {};
    S.touched = saved.touched ?? {};
    S.idx = Math.min(Math.max(saved.idx ?? 0, 0), S.cards.length - 1);
    S.view = saved.view === "list" ? "list" : "card";
    S.grades = saved.grades ?? {};
  }
  for (const a of data.answers ?? []) S.answers[a.n] = a.answer;
  $("meta").textContent = [data.deck, `${data.due} due`, data.fresh ? `${data.fresh} new` : null].filter(Boolean).join(" · ");

  if (data.stage === "input") {
    S.screen = "input";
    return show();
  }
  if (data.stage === "checking") {
    S.screen = "checking";
    if (data.gradeError) return showGradeError(data.gradeError);
    show();
    renderChecking();
    return pollResults();
  }
  try {
    showResults(await (await fetch("api/results", { cache: "no-store" })).json());
  } catch {
    showError();
  }
}

boot();

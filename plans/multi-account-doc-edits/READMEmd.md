# Exact prose edits: README.md

Base: `e576a83`. Apply/check with canonical plan_edits helper.

**E1.** (lines 32-33)

````md E1-old
The first time, the page asks you to set up an owner login, choose a grader and enter its credentials plus the AnkiConnect key:
[First start: the setup page](#first-start-the-setup-page). After that you log in on the page.
````

````md E1-new
The first time, the page asks you to set up an owner login, choose a grader and enter its credentials plus the AnkiConnect key:
[First start: the setup page](#first-start-the-setup-page). After that you log in on the page.

An existing deployment can also host invited accounts. Each account has an isolated
collection, review pass, settings and encrypted grader credentials. The owner creates
a one-use invitation in Settings; the friend registers, provides their own Claude
or Codex credential, imports cards and studies without AnkiWeb. See
[Multi-account deployment](deploy/README.md#multi-account-deployment).
````

**E2.** (lines 97-97)

````md E2-old
works there for the account that runs the server.
````

````md E2-new
works there for the account that runs the server. This fallback is available only in single-account mode.
Multi-account mode always requires your own explicit credential.
````

**E3.** (lines 155-155)

````md E3-old
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
````

````md E3-new
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `ACCOUNT_SOCKET` | empty | Opt-in private Unix socket of the separate provisioning daemon |
| `MAX_ACCOUNTS` | `4` | Total accounts including the owner, 2 to 8; daemon guest limit must agree |
````

**E4.** (lines 166-167)

````md E4-old
The server listens on 127.0.0.1 only. The page files (HTML, CSS, JS) are public; every API route except
the session, setup and login ones needs the owner's login.
````

````md E4-new
The server listens on 127.0.0.1 only. Page assets are public; study, settings, import
and media require an account login. Registration requires a valid invitation. In
multi-account mode anonymous session status exposes no account's review progress.
````

**E5.** (lines 195-196)

````md E5-old
- POST requests must be JSON, so other sites can't submit forms to it. The page runs under a strict
  Content Security Policy.
````

````md E5-new
- POST requests require JSON, except authenticated package uploads, which require
  an explicit import header and a same-origin request. The page uses a strict Content
  Security Policy; imported card documents have a separate sandbox and policy.
````

**E6.** (lines 237-239)

````md E6-old
## License

[MIT](LICENSE)
````

````md E6-new
## Inviting a friend and importing cards

Multi-account mode is opt-in through `ACCOUNT_SOCKET`; the deployment guide installs
the separate provisioning service. The owner's existing vault, pass and collection
stay in place. New accounts start empty, never require AnkiWeb and never synchronize.

In Settings the owner creates an invitation and sends its link privately. It expires
after 24 hours and can register one account. The friend opens it, chooses a unique
login and a password of at least 12 characters, selects Claude Code or Codex and
provides their own credential. Every account must provide an explicit credential in
this mode, including an owner who previously used the machine's Claude login.

Use **Import cards** to upload an `.apkg` exported from Anki with **Support older
Anki versions** enabled. Packages are limited to 8 MiB compressed, 256 MiB expanded
and 4096 entries. Split larger exports into smaller decks. Import is available when
no unfinished batch exists; finish or reset that batch first. Failed uploads keep
the saved batch, and retries merge through Anki's importer. Models, templates, media
and scheduling are imported; the page renders formatting, images and audio inside
a script-disabled frame. Card-template JavaScript is disabled. Graders receive the
plain-text card content. Knowledge-deck preferences are saved separately per account.

The browser namespaces drafts by account. API requests choose an account from its
login cookie, never from uploaded card IDs or an account query parameter. Invited
accounts cannot create invitations or configure infrastructure. Account-count and
runtime limits apply before registration; a temporarily failed registration can be
retried with the same invitation and login.

## License

[MIT](LICENSE)
````


**E7.** (lines 209-211)

````md E7-old
```sh
make check   # golangci-lint v2 and go test -race
```
````

````md E7-new
Development checks need the Go toolchain from go.mod, golangci-lint v2 and Node.js
18 or newer for browser-state tests.

```sh
make check   # lint, Go race tests, deployment fixtures and browser-state tests
```
````

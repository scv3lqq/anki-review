# Security policy

## Reporting a vulnerability

Please report privately: open the repository's **Security** tab and choose **Report a vulnerability**.
Don't open a public issue for it. You'll get an answer within a week.

## Supported versions

Only the latest commit on `main` is supported.

## Design notes

What the server is built to withstand, so a report can be checked against it:

- It binds to 127.0.0.1. Exposing it to a network without an access-controlling reverse proxy is out
  of scope.
- One owner logs in on the page. Every API route except session, setup and login needs the session
  cookie (`HttpOnly`, `SameSite=Strict`, in memory, 30 days). Password checks and failed setup checks
  are throttled per client (the last `X-Forwarded-For` hop, else `X-Real-IP`, else the peer address):
  the first 5 failures are free, then the wait doubles up to 15 minutes. Settings have their own count.
- The Claude token and the AnkiConnect key are sealed in `DATA_DIR/vault.json` (XChaCha20-Poly1305,
  key from Argon2id of the owner's password, fresh salt and nonce per save). They are plaintext only
  in the server's memory, after a login. A restart locks the server.
- Setup checks the AnkiConnect key against the local Anki before anything else. The deploy kit always
  sets a key, so someone who reaches a fresh server can't set it up without that key. An AnkiConnect
  without an apiKey accepts any setup: keep it on a machine you control.
- Requests whose `Host` is not `127.0.0.1` or `localhost` get 403 (DNS rebinding).
- State-changing requests are POST with a JSON body only (CSRF through simple form posts).
- Answer keys are not served before the answers are graded.
- `claude` runs with `--safe-mode --tools ""` and gets the prompt on stdin; card text is treated as data.
- The state file holds answer keys and is created `0600` in a `0700` directory.
- Out of scope: root or the service user on the machine that runs the server (they can read the
  server's memory while it is unlocked), and the copy of the AnkiConnect key the deploy kit keeps for
  the Anki container.
- The deploy kit's self-hosted mode runs Anki's own sync server on 127.0.0.1. Devices reach it through
  the owner's HTTPS proxy, and its login (a random password in the deploy dir, 0600) is its only access
  control: whoever has the password can read or replace the collection.
- No secrets live in this repository. The tokens and the owner's password are typed into the page and
  stay on the machine that runs the server.

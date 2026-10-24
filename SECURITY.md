# Security policy

## Reporting a vulnerability

Please report privately: open the repository's **Security** tab and choose **Report a vulnerability**.
Don't open a public issue for it. You'll get an answer within a week.

## Supported versions

Only the latest commit on `main` is supported.

## Design notes

What the server is built to withstand, so a report can be checked against it:

- It binds to 127.0.0.1 and has no authentication. Exposing it to a network without an access-controlling
  reverse proxy is out of scope.
- Requests whose `Host` is not `127.0.0.1` or `localhost` get 403 (DNS rebinding).
- State-changing requests are POST with a JSON body only (CSRF through simple form posts).
- Answer keys are not served before the answers are graded.
- `claude` runs with `--safe-mode --tools ""` and gets the prompt on stdin; card text is treated as data.
- The state file holds answer keys and is created `0600` in a `0700` directory.
- No secrets live in this repository. `ANKI_CONNECT_KEY` and the Claude Code login stay in the
  environment of the machine that runs the server.

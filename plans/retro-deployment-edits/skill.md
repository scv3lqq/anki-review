**E1.** (lines 25-29)

````md E1-old
- Codex performs authorized setup steps itself. The user types the AnkiWeb password or pastes the self-hosted sync
  password (server), the sudo password (server, once) and the setup form on the page: owner login,
  owner password, selected provider credentials, AnkiConnect key. Automate the non-secret parts of installation and
  login when tools permit; leave secret entry to the user. Do not ask again for actions already
  authorized by the setup request. Ask only for missing choices or new consequential actions.
````

````md E1-new
- Codex performs authorized setup steps itself. The owner enters the AnkiWeb/sync login
  and the setup form: owner login, owner password, selected provider credentials and
  AnkiConnect key. For sudo, first follow the credential route in
  [Settings and access](references/server.md#settings-and-access): an explicitly authorized
  existing password-store entry can be streamed without displaying it. Otherwise the
  owner enters sudo once in a verified visible terminal. Do not ask again for actions
  already authorized by the setup request. Ask only for missing choices or new
  consequential actions.
````

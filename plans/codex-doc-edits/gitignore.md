# Mechanical edits: .gitignore

Base: 3422c81bf8c0a9a57d99dd3a2f43bb0c24a862bc. Generated with the canonical plan_edits helper.

**E1.** (lines 17-22)

````md E1-old
/deploy/anki/secrets/
/deploy/bin/
/deploy/claude-token
/deploy/install-state
/deploy/.install-state.*

````

````md E1-new
/deploy/anki/secrets/
/deploy/bin/
/deploy/claude-token
# Dedicated OAuth staging must never be committed.
**/auth.json
**/.codex/
**/anki-review-login.*/
**/anki-review-import.*/
/deploy/install-state
/deploy/.install-state.*

````

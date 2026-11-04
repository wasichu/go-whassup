# go-whassup docs

Design notes and hard-won protocol facts. Keep these in sync with the code: a
change to behavior described here updates the doc **in the same commit**.

| Doc | Covers |
| --- | --- |
| [architecture.md](architecture.md) | Package layering, event flow, goroutine boundaries |
| [pairing.md](pairing.md) | Phone-code pairing sequence and WhatsApp's server-side restrictions |
| [storage.md](storage.md) | Where data lives, the session DB, SQLite settings |
| [usage.md](usage.md) | Commands and flags |
| [voice.md](voice.md) | Native voice messages, audio backends, draft lifecycle, and validation |
| [config.md](config.md) | Config file, design, and every setting |
| [roadmap.md](roadmap.md) | Where we are and what's next |
| [dependencies.md](dependencies.md) | Module/tool/Action updates, weekly whatsmeow PRs, and vulnerability scans |
| [homebrew.md](homebrew.md) | Plan for `brew install` from our own tap |

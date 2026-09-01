# Contributing

Pull requests are welcome when they preserve the permanent safety boundaries:

- Mineflayer observes order menus and never performs an economic action.
- Unknown menus, items, or outcomes fail closed.
- API keys, Microsoft caches, proxy credentials, raw chat, inventories, NBT, databases, and player identities are never committed.
- Fabric transaction work requires explicit session consent and live economic/screen verification.

Before opening a pull request, run:

```text
go test ./...
go vet ./...
cd collector && npm ci && npm test && npm audit --omit=dev
gradle -p minecraft/client-mod clean test build
```

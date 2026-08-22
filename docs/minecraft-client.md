# Minecraft client

The Fabric module targets Minecraft 1.21.11, Fabric Loader 0.19.2 or newer, Yarn `1.21.11+build.6`, Fabric API `0.141.6+1.21.11`, and Java 21. The auction observer compiles and its core tests run against that baseline; Donut’s exact live title and lore variants still require fixture validation.

`ClientCore` owns a lock-free immutable price cache, exact-to-base local fallback, hot-path evaluator, and bounded asynchronous telemetry queue. A version-specific adapter must project Minecraft `ItemStack` state into `ItemStackView` only when a relevant screen slot changes. No tick polling is registered.

The shipped adapter is notify-only. It observes `HandledScreen` lifecycle events, scans only server-owned slots on the screen tick, fingerprints each stack, and reparses only changed slots. It never clicks a slot or sends a Minecraft command. `PurchaseController` retains assisted, simulated and live-interaction boundaries for future work; before any ordinary interaction, an adapter must revalidate screen sync ID, slot, exact signature, price and seller. The project intentionally contains no bypass, stealth, exploit or evasion behavior.

Price evaluation is local. Exact-signature listings on the current screen form a bounded order book; a candidate is compared with the median of the other visible listings once at least three comparables exist. When a fresh official/backend snapshot is available it takes precedence, while the result retains both the screen median and official quick-sell total plus their basis-point spread. No network request occurs in the screen parsing or decision path.

See [auction-parser.md](auction-parser.md) for recognized screen/lore shapes, validation steps, and the benchmark command.

Commands intended for a later UI layer are `/dn`, `/dn chat`, `/dn price <item>`, `/dn stats`, `/dn workers`, and `/dn network`. Command rendering and network snapshot transport remain future work.

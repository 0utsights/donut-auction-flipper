# Architecture

```text
Mineflayer observer accounts -- order observations/tasks --+
                                                           |
Official Donut auction API -- fast + broad valuation ------+--> Go backend
                                                                  |
                                                        scored candidate pool
                                                                  |
                                                        ETag HTTP polling
                                                                  |
                                                                  v
                                            Fabric local balance/slot allocator
                                                                  |
                                                     manual /orders or /ah use
```

## Permanent boundaries

Mineflayer is the only in-game **market parser**. It reads order menus and never buys, fulfills, creates, confirms, cancels, claims, lists, or transfers inventory. The official API is the only auction-data source. Fabric does not upload market prices; it consumes candidates and keeps personal balance, reserve, slot allocation, session execution budget, and pending submissions local. A Fabric order requires a visible one-order arm and fail-closed validation; claiming and auction relisting are deferred until their live workflows are captured.

The backend and collector run together on the private host. Fabric connects over authenticated HTTPS. There are no WebSockets.

## Auction model

The existing two API lanes remain intact:

1. The fast lane refreshes the newest auction page.
2. The broad lane refreshes completed transactions and the configured recent active-book window.
3. The immutable `robust-v4-price-volume-quantity` engine requires singular and exact-resale-quantity evidence for stacks.
4. Confidence, liquidity, and sell time count only completed sales within ±10% of the intended resale price and require independent sellers.

The API key never leaves the backend.

## Observer coordination

The Node manager starts one isolated child process per configured Microsoft account. Each uses its own profile directory and proxy. Proxy egress is verified before login.

Observers register and long-poll leased tasks:

- `discovery`: crawl the order market.
- `focused_watch`: refresh an item selected by a Fabric portfolio.
- `verification`: independently sample another observer's evidence.
- `schema_probe`: capture an unknown layout without clicking.

Direct page ranges are used only when a verified GUI supports them. Otherwise, one or more observers perform discovery while additional observers handle focused/verification tasks. Duplicate submissions are harmless because scans are keyed by observer, session, and content hash. Lease heartbeats prevent healthy long scans from being reassigned.

Unknown screens fail closed. A checked-in schema must match title, listing slots, and exact control fingerprints. Unknown, incomplete, and capture-only rows are retained for coverage diagnostics but cannot enter price or fill evidence. The generic parser also marks canonical modifier equivalence incomplete; a fixture-specific, versioned mapping is required before evidence can become actionable. The collector's narrow navigation wrapper is the only code allowed to call chat/window APIs.

## Order evidence and candidates

SQLite WAL retains complete/incomplete scans, normalized order rows, proven quantity decreases, observers, leases, watches, diagnostics, and candidate evidence. Raw scans expire after seven days, derived fills after 90 days, diagnostics after 14 days, and daily database backups after seven days.

An order disappearance is not a fill. A fill event requires a decrease in the same observer/order key. Evidence graduates through:

- `captured`: at least one parsed observation.
- `research`: at least three independent menu sessions spanning ten seconds.
- `actionable`: at least five sessions and five bounded focused-watch reductions across three stable keys spanning 30 seconds, stable current pricing, no observer conflict, and a fresh snapshot.

Combined actionability also requires five exact-quantity, near-target auction sales from three sellers in 24 hours, at least 50% model confidence, and target-price sale evidence no older than two hours.

Routes are:

- `ORDER_TO_AUCTION`: competitive order acquisition, unchanged-quantity auction exit.
- `AUCTION_TO_ORDER`: current auction acquisition, existing order exit.

Each candidate includes fees, capital, market/inventory slots, completion probability, expected cycle, executable batches, and:

`risk-adjusted profit/day = conservative profit × completion probability ÷ cycle days`

No absolute order-auction profit floor exists.

Priority is the risk-adjusted profit/day multiplied by conservative attainable batch capacity, capped by the 18 auction-slot limit. Only signature-complete base commodities receive a priority rank. Exact full-stack auction evidence is preferred; single-item evidence is a fallback only when no exact stack cohort exists. One acquisition order may fill several identical resale batches and therefore consumes one order slot while each relisted batch consumes one auction slot.

Outstanding quantity in other players' buy orders is demand, not sell-side fill capacity. It is shown as context and may bound the immediate auction-to-existing-order route, but cannot scale a new order-to-auction position. An uncalibrated order-to-auction research idea receives one exploratory batch; only confirmed short-gap fill velocity unlocks multi-batch capacity.

Donut's live menu does not expose owner/order identity on the focused rows. Fill evidence is therefore trusted at level one only when the same observer sees the same target signature, page, slot-derived stable key, requested quantity, and unit reward decrease within two minutes of the same leased focused-watch task. A server-visible owner/order ID upgrades it to level two. Discovery, neighboring rows, cross-page matches, disappearances, and older inferred reductions remain at level zero and do not contribute volume, graduation, completion estimates, or ranking. Discovery pagination contributes one price/evidence sample per session; focused refreshes contribute separate sessions, so later price tiers cannot masquerade as volatility.

## Fabric allocation

The backend publishes at most 100 scored candidates. Fabric filters to `READY` candidates and solves an integer portfolio locally under a 100 ms budget.

Constraints:

- current balance after a dynamic 15–35% reserve;
- 20 minus locally used order slots;
- 18 minus locally used auction slots;
- conservative executable batches;
- 25% deployable-capital exposure per exact signature;
- 40% per base item.

Profit per inventory slot is a tie-breaker, not a hard capacity constraint. The valuable market valuation stays on the backend; only resource allocation is local.

For a selected `ORDER_TO_AUCTION` candidate, the Fabric executor may create exactly one order after a separate local arm screen. The captured candidate must remain stable on item signature, quantity, cent-precise reward, escrow, and auction target; the feed, order observation, and auction exit must remain fresh. The executor identifies the two chest menus by title and slot label, then uses the 1.21.11 server-dialog model for item search, amount, price, review, and final action. Unknown or ambiguous state closes the workflow without clicking. The final review must contain the expected item, quantity, rounded-or-exact unit reward, and total before `Create Order` is invoked once.

## Trust boundaries

- Environment tokens are hashed in memory before request handling and must be distinct when configured.
- Observer, Fabric, and administrative routes use separate credentials/scopes.
- Collector account tokens and proxy credentials never enter backend storage.
- Fabric diagnostics use a fixed allowlist and omit personal/game content.
- Backend commands and Fabric commands are independently validated.
- Debug pages contain real source state only; no simulated market rows are possible.

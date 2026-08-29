# Donut order-creation workflow

Creation was verified against the live DonutSMP 1.21.11 client on 2026-08-29. A real Netherite Scrap order was created and proven in `Your Orders`; the public-rank/reprice code is fixture-tested but has not been allowed to cancel that valuable live order. The exact `Orders -> Edit Order` container was captured non-transactionally on 2026-08-29.

## Player flow

1. Run `/orders`.
2. In `Orders (Page 1)`, click `Your Orders`, the dark block at slot 51. Do not confuse it with the purple shard at slot 48, which opens `Shard Shop`.
3. In `Orders -> Your Orders`, click the first black placeholder pane labeled for order creation. With no active personal orders, this was slot 0; Fabric searches only the server-menu inventory for the first black-pane control whose label contains both `create` and `order`, so active rows cannot be mistaken for the creation entry.
4. In `Choose Item`, enter the registry path in `Search` (`diamond_block`, not `diamond block`), submit it, and choose one exact result. Fabric verifies the returned result against the canonical registry ID even when Donut displays a reordered name such as `Block of Diamond`.
5. In `How many?`, enter the total requested unit count. The default is `1`.
6. In `Price per item?`, enter the unit reward. The screen displays the amount and a minimum price of `$1`. In the live sample it prefilled `$910K` for one diamond block.
7. Click `Review Order`.
8. In `Review Order`, verify the canonical item, amount, unit price, and total escrow. The live sample displayed `Block of Diamond`, amount `1`, `$910K each`, and `$910K total`. The screen offers `Change Item`, `Change Amount`, `Change Price`, `Cancel`, and `Create Order`.
9. `Create Order` is the final creation action. Fabric clicked it only after the reviewed values matched, then proved the resulting personal order.

The review total observed was exactly `amount × unit price`; no additional creation fee was displayed. This must be revalidated with a low-value live order before relying on it because the server can change its economy rules.

## Fabric automation contract

Model the wizard as a fail-closed state machine rather than coordinate macros:

`ORDER_BOARD -> YOUR_ORDERS -> ITEM_PICKER -> AMOUNT -> UNIT_PRICE -> REVIEW -> FINAL_REVALIDATION -> CREATE`

For every transition, Fabric must identify the expected screen from its title and visible controls, then verify the resulting screen before continuing. In a continuous automatic session, a failure proven to occur before `Create Order` quarantines only that canonical item and advances to unrelated work. An unknown shared navigation screen, disconnect, unexpected balance change, or any ambiguous result after submission still stops the session.

Before taking the final action, Fabric must verify:

- The backend candidate is still `READY`, the auction valuation is fresh, and an explicitly focused Mineflayer observation—not merely a later discovery page—was received within six seconds.
- The canonical item signature and selected result match.
- Amount equals the allocator's checked total across one or more exact resale batches. Each eventual auction listing still uses the backend's exact batch quantity.
- Unit reward equals the prepared competitive price; suffix-formatted input such as `910K` must be parsed back to an exact integer before acceptance.
- Review total equals checked integer multiplication of amount and unit price.
- Review total is within the user's remaining session budget, deployable balance, reserve, exposure, and order-slot limits.
- Auction exit valuation is still current and profitable after fees.
- No personal order for the same canonical item is already active or pending locally. Fabric persists submitted item IDs across restarts and inspects the verified `Your Orders` inventory for an exact registry-ID match before it opens the creation wizard.

Because the exact success/failure chat fixtures are not yet captured, pressing the server action records a conservative local pending position rather than claiming success. The same item cannot be allocated again across client restarts. Direct duplicate-order server messages fail closed and persist the item lock. The player can clear local locks with `Reset order cache`; this only makes the item eligible for review again, and the exact personal-order menu check still blocks creation if the order remains present.

The Fabric executor supports two explicit consent scopes. `ARM ONE ORDER` authorizes exactly one portfolio selection. `ENABLE AUTO ORDERS` authorizes the currently allocated queue for the current Minecraft session and is disabled by default. A selection combines its allocated resale batches into one acquisition order for that item; it never creates duplicate per-stack orders. Before each queued order, Fabric obtains a new focused observation and repeats every review invariant. It initially bids one cent above the observed display bucket while freezing the backend's conservative bucket boundary as the maximum escrow. After clicking `Create Order`, it reopens the verified personal-order menu and requires one exact canonical item, unit-price bucket, requested quantity, and zero-delivered progress match.

Fabric then reopens the global board, proves `Most Per Item` from descending unit rewards, searches the underscore-delimited canonical path, and identifies the submitted order by canonical item, exact requested quantity, zero-delivered progress, and its visible price bucket. A unique rank of one completes the workflow. A lower rank opens the exact live-captured `Orders -> Edit Order` screen, permits only one semantic `Cancel Order` control, accepts only a cancellation-specific confirmation, and then proves the item is absent from `Your Orders`. Partial fills and ambiguous rows are never cancelled. The scoreboard must confirm the refund before a replacement can start. The replacement uses the competitive display-bucket boundary and is created only when its combined escrow stays below the consent-time cap and its recalculated exit profit stays above the frozen floor. If the boundary still does not reach rank one, or another increase would violate either bound, the order is cancelled and the item is quarantined for the automatic session. Explicit duplicate rejection and other proven pre-submit item-local failures are handled the same way. Unknown shared screens, missing or duplicate post-submit matches, stale/changed state after submission, disconnects, refund uncertainty, and balance inconsistencies stop the session and leave uncertain state locked for manual review. Mineflayer remains observation-only and never enters `Your Orders`, the creation wizard, or any transactional screen.

## Completed-order auction exit

Automatic exits have their own session consent and never inherit `Auto Orders` authority. Fabric persists one position per canonical item with delivered, claimed, packaged, and listed totals. A completely filled vanilla-signature position is eligible only when its exact item, requested quantity, unit-reward bucket, and completed progress appear once in `Orders -> Your Orders` and again in `Orders -> Edit Order`; only the exact `Collect` chest at slot 13 is allowed.

Fabric collects one planned exit package at a time. Below 27 physical inventory slots, each backend-proven candidate batch is assembled and relisted unchanged. At 27 or more slots, Fabric buys generic empty shulkers from a fresh backend supply quote only after verifying the live `Lowest Price` row and a balance-scaled cap. The player must look at a safe placement space. Fabric places one box, loads only the exact plain item and quantity, verifies no other contents, closes and recovers that exact box, then runs `/auction sell <price>` and accepts only a matching confirmation. A listing counts only after exactly one item/contents/quantity/price match appears in `Your Items`.

Unknown screens, partial transfers, modified items, duplicate rows, stale supply, unexpected inventory changes, unsafe placement, missing receipts, or auction-slot exhaustion pause or stop without guessing. A shulker left placed during a failure is reported with its coordinates for manual recovery. Mineflayer never claims, buys a box, packages, or lists; any future port must reject these shulker-dependent orders before acquisition.

## Data still required for safe live acceptance

- Fabric screen/widget identifiers for all five wizard screens; the live discovery established the visible contract but not stable internal widget IDs.
- Behavior for invalid, fractional, suffix-formatted, minimum, maximum, and overflow amounts/prices.
- Whether creation fees exist but are omitted from the review screen.
- Exact server responses for insufficient funds, slot exhaustion, price changes, disconnects, and timeouts. Post-submit success is proven from exact personal and public-menu evidence rather than chat text.
- The live cancellation confirmation layout still needs a deliberately low-value acceptance fixture. The management container title (`Orders -> Edit Order`) was captured without pressing its red cancellation control; changed confirmation layouts remain fail-closed.
- Personal-order row schema, filled-item collection flow, cancellation/refund behavior, and expiry.
- A low-value, explicitly armed end-to-end rank/reprice acceptance order, including cancellation confirmation and scoreboard refund proof, before enabling replacement on normal-budget orders.

## Acceptance test sequence

Use a deliberately low-cost base item on an account with a deliberately limited balance. Capture each screen transition, verify the debited balance equals the review total, confirm the order appears once in `Your Orders`, then manually cancel it and verify the refund. Do not fund a larger acceptance test until these checks and the known failure cases pass.

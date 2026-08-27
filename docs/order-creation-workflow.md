# Donut order-creation workflow

Verified against the live DonutSMP 1.21.11 client on 2026-08-25. The exploration stopped at the review screen and did not create an order or spend currency.

## Player flow

1. Run `/orders`.
2. In `Orders (Page 1)`, click `Your Orders`, the dark block at slot 51. Do not confuse it with the purple shard at slot 48, which opens `Shard Shop`.
3. In `Orders -> Your Orders`, click the first black placeholder pane labeled for order creation. With no active personal orders, this was slot 0; Fabric searches only the server-menu inventory for the first black-pane control whose label contains both `create` and `order`, so active rows cannot be mistaken for the creation entry.
4. In `Choose Item`, enter the registry path in `Search` (`diamond_block`, not `diamond block`), submit it, and choose one exact result. Fabric verifies the returned result against the canonical registry ID even when Donut displays a reordered name such as `Block of Diamond`.
5. In `How many?`, enter the total requested unit count. The default is `1`.
6. In `Price per item?`, enter the unit reward. The screen displays the amount and a minimum price of `$1`. In the live sample it prefilled `$910K` for one diamond block.
7. Click `Review Order`.
8. In `Review Order`, verify the canonical item, amount, unit price, and total escrow. The live sample displayed `Block of Diamond`, amount `1`, `$910K each`, and `$910K total`. The screen offers `Change Item`, `Change Amount`, `Change Price`, `Cancel`, and `Create Order`.
9. `Create Order` is the final economic action. It was not clicked during discovery.

The review total observed was exactly `amount × unit price`; no additional creation fee was displayed. This must be revalidated with a low-value live order before relying on it because the server can change its economy rules.

## Fabric automation contract

Model the wizard as a fail-closed state machine rather than coordinate macros:

`ORDER_BOARD -> YOUR_ORDERS -> ITEM_PICKER -> AMOUNT -> UNIT_PRICE -> REVIEW -> FINAL_REVALIDATION -> CREATE`

For every transition, Fabric must identify the expected screen from its title and visible controls, then verify the resulting screen before continuing. Any unknown title, missing control, ambiguous item result, server message, disconnect, or unexpected balance change cancels the workflow.

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

The Fabric executor may press the final `Create Order` control only for one portfolio selection that the player explicitly armed in the local UI. A selection combines its allocated resale batches into one acquisition order for that item; it never creates duplicate per-stack orders. Arming must be disabled by default, visibly identify the item, total quantity, resale-stack count, maximum escrow, and session budget, and expire if any screen, value, candidate, allocation, or freshness check changes. Immediately before the click, Fabric must repeat every review invariant and cancel on uncertainty. One arm authorizes one order attempt; it must never loop or silently arm another candidate. Mineflayer remains observation-only and must never enter `Your Orders`, the creation wizard, or any transactional screen.

## Data still required for safe live acceptance

- Fabric screen/widget identifiers for all five wizard screens; the live discovery established the visible contract but not stable internal widget IDs.
- Behavior for invalid, fractional, suffix-formatted, minimum, maximum, and overflow amounts/prices.
- Whether creation fees exist but are omitted from the review screen.
- Exact server responses for success, insufficient funds, slot exhaustion, price changes, duplicate orders, disconnects, and timeouts.
- Personal-order row schema, filled-item collection flow, cancellation/refund behavior, and expiry.
- A low-value, explicitly armed end-to-end acceptance order followed by manual cancellation/refund verification before enabling normal-budget order creation.

## Acceptance test sequence

Use a deliberately low-cost base item on an account with a deliberately limited balance. Capture each screen transition, verify the debited balance equals the review total, confirm the order appears once in `Your Orders`, then manually cancel it and verify the refund. Do not fund a larger acceptance test until these checks and the known failure cases pass.

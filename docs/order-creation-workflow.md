# Donut order-creation workflow

Verified against the live DonutSMP 1.21.11 client on 2026-08-25. The exploration stopped at the review screen and did not create an order or spend currency.

## Player flow

1. Run `/orders`.
2. In `Orders (Page 1)`, click `Your Orders`, the dark block at slot 51. Do not confuse it with the purple shard at slot 48, which opens `Shard Shop`.
3. In `Orders -> Your Orders`, click the first black placeholder pane at slot 0. With no active personal orders, this is the new-order entry.
4. In `Choose Item`, enter a display-name query in `Search`, submit it, and choose one exact result. A search for `Diamond Block` returned `Block of Diamond`.
5. In `How many?`, enter the total requested unit count. The default is `1`.
6. In `Price per item?`, enter the unit reward. The screen displays the amount and a minimum price of `$1`. In the live sample it prefilled `$910K` for one diamond block.
7. Click `Review Order`.
8. In `Review Order`, verify the canonical item, amount, unit price, and total escrow. The live sample displayed `Block of Diamond`, amount `1`, `$910K each`, and `$910K total`. The screen offers `Change Item`, `Change Amount`, `Change Price`, `Cancel`, and `Create Order`.
9. `Create Order` is the final economic action. It was not clicked during discovery.

The review total observed was exactly `amount × unit price`; no additional creation fee was displayed. This must be revalidated with a low-value live order before relying on it because the server can change its economy rules.

## Fabric automation contract (design only; live execution paused)

DonutSMP's published terms currently prohibit macros/scripts and unfair-advantage modifications. The state machine below is retained as a testable design, but it must not be enabled on DonutSMP without explicit written staff authorization. Until then, Fabric may show the values and checklist while the player performs every interaction manually.

Model the wizard as a fail-closed state machine rather than coordinate macros:

`ORDER_BOARD -> YOUR_ORDERS -> ITEM_PICKER -> AMOUNT -> UNIT_PRICE -> REVIEW -> PLAYER_CONFIRM`

For every transition, Fabric must identify the expected screen from its title and visible controls, then verify the resulting screen before continuing. Any unknown title, missing control, ambiguous item result, server message, disconnect, or unexpected balance change cancels the workflow.

Before exposing the final confirmation, Fabric must verify:

- The backend candidate is still `READY` and fresh.
- The canonical item signature and selected result match.
- Amount equals the backend's exact resale quantity.
- Unit reward equals the prepared competitive price; suffix-formatted input such as `910K` must be parsed back to an exact integer before acceptance.
- Review total equals checked integer multiplication of amount and unit price.
- Review total is within the user's remaining session budget, deployable balance, reserve, exposure, and order-slot limits.
- Auction exit valuation is still current and profitable after fees.
- No equivalent personal order is already active or pending locally.

The permitted implementation should prepare the values and require the player to perform every navigation and transaction click. A later Fabric-only executor may exist only after explicit server authorization, a separately visible arming step, and a last-moment revalidation. Mineflayer must never enter `Your Orders`, the creation wizard, or any transactional screen.

## Data still required before execution automation

- Fabric screen/widget identifiers for all five wizard screens; the live discovery established the visible contract but not stable internal widget IDs.
- Behavior for invalid, fractional, suffix-formatted, minimum, maximum, and overflow amounts/prices.
- Whether creation fees exist but are omitted from the review screen.
- Exact server responses for success, insufficient funds, slot exhaustion, price changes, duplicate orders, disconnects, and timeouts.
- Personal-order row schema, filled-item collection flow, cancellation/refund behavior, and expiry.
- A low-value end-to-end acceptance order followed by cancellation/refund verification.

## Acceptance test sequence

Use a deliberately low-cost base item and cap the test escrow independently of the normal flipping budget. Capture each screen transition, verify the debited balance equals the review total, confirm the order appears once in `Your Orders`, then manually cancel it and verify the refund. Do not enable automated `Create Order` until this test and the failure cases above pass.

package net.donutnetwork.client;

import net.minecraft.client.gui.DrawContext;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.client.gui.tooltip.Tooltip;
import net.minecraft.client.gui.widget.ButtonWidget;
import net.minecraft.text.Text;

import java.util.List;

/** Plain local portfolio/debug UI. It never invokes Minecraft's blur background. */
final class DonutScreen extends Screen {
    private static final int WIDTH = 440;
    private static final int HEIGHT = 240;
    private static final int PAGE_SIZE = 4;
    private final Screen parent;
    private final FlipFeedClient auctions;
    private final CandidateFeedClient candidates;
    private final OrderCreationExecutor orderExecutor;
    private String renderedKey = "";
    private int portfolioPage;

    DonutScreen(Screen parent, FlipFeedClient auctions, CandidateFeedClient candidates, OrderCreationExecutor orderExecutor) {
        super(Text.literal("Donut market flipper")); this.parent = parent; this.auctions = auctions; this.candidates = candidates; this.orderExecutor = orderExecutor;
    }

    @Override protected void init() {
        int left = (width - WIDTH) / 2;
        int top = Math.max(8, (height - HEIGHT) / 2);
        addDrawableChild(ButtonWidget.builder(Text.literal("Balance -$1m"), button -> { candidates.adjustBalance(-1_000_000); clearAndInit(); })
                .tooltip(Tooltip.of(Text.literal("Reduce the local planning balance by $1 million. This never changes your server balance.")))
                .dimensions(left, top + 40, 106, 20).build());
        addDrawableChild(ButtonWidget.builder(Text.literal("Balance +$1m"), button -> { candidates.adjustBalance(1_000_000); clearAndInit(); })
                .tooltip(Tooltip.of(Text.literal("Increase the local planning balance by $1 million. Labeled balance chat can update it automatically.")))
                .dimensions(left + 110, top + 40, 106, 20).build());
        addDrawableChild(ButtonWidget.builder(Text.literal("Orders " + candidates.usedOrderSlots() + "/20"), button -> { candidates.adjustUsedSlots(client.isShiftPressed() ? 1 : -1, 0); clearAndInit(); })
                .tooltip(Tooltip.of(Text.literal("Orders currently active. Click after cancelling one to subtract a used slot; Shift-click to add one.\nTracked item locks remain a safe lower bound."))).dimensions(left + 220, top + 40, 106, 20).build());
        addDrawableChild(ButtonWidget.builder(Text.literal("Auctions " + candidates.usedAuctionSlots() + "/18"), button -> { candidates.adjustUsedSlots(0, client.isShiftPressed() ? 1 : -1); clearAndInit(); })
                .tooltip(Tooltip.of(Text.literal("Listings currently active. Click to subtract one; Shift-click to add one. Large orders reuse these slots as ≤64-item exits sell."))).dimensions(left + 330, top + 40, 110, 20).build());

        List<PortfolioAllocator.Selection> selected = candidates.allocation().selections();
        int pageCount = Math.max(1, (selected.size() + PAGE_SIZE - 1) / PAGE_SIZE);
        portfolioPage = Math.max(0, Math.min(portfolioPage, pageCount - 1));
        int firstSelection = portfolioPage * PAGE_SIZE;
        for (int row = 0; row < Math.min(PAGE_SIZE, selected.size() - firstSelection); row++) {
            PortfolioAllocator.Selection selection = selected.get(firstSelection + row);
            CandidateFeedClient.Candidate candidate = selection.candidate();
            String label = (isFiller(candidate) ? "FILL " : "CORE ") + abbreviate(candidate.itemName(), 10) + " · " + compact(selection.orderQuantity()) + " units / "
                    + compact(selection.batches()) + "×" + candidate.quantity() + " exits · +$" + FlipNotifier.format(selection.conservativeProfit());
            addDrawableChild(ButtonWidget.builder(Text.literal(label), button -> CandidateNotifier.open(client, candidates, candidate))
                    .tooltip(Tooltip.of(Text.literal((isFiller(candidate)
                                    ? "FILLER OFFER\nOne conservative exit stack. It may fill slowly; cancel and replace it when a stronger CORE market appears.\n"
                                    : "CORE OFFER\nMeasured order fills support this scalable position.\n")
                            + "ONE BUY ORDER\n" + selection.orderQuantity() + " units at " + formatCents(candidate.orderUnitRewardCents())
                            + " each · escrow $" + FlipNotifier.format(selection.capital()) + "\nEXIT PLAN\n" + selection.batches()
                            + " listings of " + candidate.quantity() + " at $" + FlipNotifier.format(candidate.targetListPrice())
                            + " each · reuse auction slots as they sell\nMODEL\nconservative +$" + FlipNotifier.format(selection.conservativeProfit())
                            + " · risk-adjusted $" + FlipNotifier.format(selection.riskAdjustedProfitDay()) + "/day · margin "
                            + candidate.marginBps() / 100.0 + "% · confidence " + candidate.confidenceBps() / 100.0
                            + "% · completion " + candidate.completionBps() / 100.0 + "% · cycle " + candidate.expectedCycleMinutes() + "m")))
                    .dimensions(left, top + 92 + row * 22, WIDTH - 98, 20).build());
            ButtonWidget arm = ButtonWidget.builder(Text.literal("Review order"), button -> client.setScreen(new OrderArmScreen(this, orderExecutor, selection)))
                    .tooltip(Tooltip.of(Text.literal("Open the final explanation and arm exactly one bulk order. Your Orders is checked for this item first.")))
                    .dimensions(left + WIDTH - 94, top + 92 + row * 22, 94, 20).build();
            arm.active = candidate.route().equals("ORDER_TO_AUCTION") && !orderExecutor.status().active();
            addDrawableChild(arm);
        }

        addDrawableChild(ButtonWidget.builder(Text.literal("Chat alerts: " + (auctions.alertsEnabled() ? "ON" : "OFF")), button -> { auctions.setAlertsEnabled(!auctions.alertsEnabled()); clearAndInit(); })
                .tooltip(Tooltip.of(Text.literal("Includes immediate API-auction alerts, which remain in chat to keep this order screen concise.")))
                .dimensions(left, top + 188, 144, 20).build());
        addDrawableChild(ButtonWidget.builder(Text.literal("Diagnostics: " + (candidates.diagnosticsEnabled() ? "ON" : "OFF")), button -> { candidates.setDiagnostics(!candidates.diagnosticsEnabled()); clearAndInit(); })
                .dimensions(left + 148, top + 188, 144, 20).build());
        if (orderExecutor.status().active()) {
            addDrawableChild(ButtonWidget.builder(Text.literal("STOP ORDER"), button -> { orderExecutor.cancel(client, "cancelled by player"); clearAndInit(); })
                    .dimensions(left + 296, top + 188, 144, 20).build());
        } else addDrawableChild(ButtonWidget.builder(Text.literal("Close"), button -> close()).dimensions(left + 296, top + 188, 144, 20).build());
        ButtonWidget previous = ButtonWidget.builder(Text.literal("← Orders"), button -> { portfolioPage--; clearAndInit(); })
                .tooltip(Tooltip.of(Text.literal("Show the previous four planned acquisition orders."))).dimensions(left, top + 212, 106, 20).build();
        previous.active = portfolioPage > 0;
        addDrawableChild(previous);
        ButtonWidget next = ButtonWidget.builder(Text.literal("Orders →"), button -> { portfolioPage++; clearAndInit(); })
                .tooltip(Tooltip.of(Text.literal("Show the next four planned acquisition orders."))).dimensions(left + 110, top + 212, 106, 20).build();
        next.active = portfolioPage + 1 < pageCount;
        addDrawableChild(next);
        addDrawableChild(ButtonWidget.builder(Text.literal("Recheck tracked item locks (" + candidates.activeOrderCount() + ")"), button -> {
                    candidates.recheckTrackedOrders(); clearAndInit();
                }).tooltip(Tooltip.of(Text.literal("After manually cancelling/replacing offers in Donut, clear local locks and rebuild the 20-slot plan.\nThe mod starts focused rechecks, then still opens Your Orders and blocks any duplicate item.")))
                .dimensions(left + 220, top + 212, 220, 20).build());
        renderedKey = feedKey();
    }

    @Override public void tick() { super.tick(); if (!renderedKey.equals(feedKey())) clearAndInit(); }

    @Override public void render(DrawContext context, int mouseX, int mouseY, float delta) {
        context.fill(0, 0, width, height, 0xFF101010);
        int left = (width - WIDTH) / 2; int top = Math.max(8, (height - HEIGHT) / 2);
        context.drawCenteredTextWithShadow(textRenderer, title, width / 2, top, 0xFFFFFF);
        PortfolioAllocator.Allocation portfolio = candidates.allocation();
        context.drawTextWithShadow(textRenderer, Text.literal("Balance $" + FlipNotifier.format(portfolio.balance()) + " (" + candidates.balanceSource() + ") · deployable $"
                + FlipNotifier.format(portfolio.deployable()) + " · reserve " + portfolio.reserveBps() / 100.0 + "%"), left, top + 14, 0xDDDDDD);
        context.drawTextWithShadow(textRenderer, Text.literal("Orders: " + candidates.status().state() + " · CORE " + readyCount(false) + " FILLER " + readyCount(true)
                + " HOLD " + stateCount("HOLD") + " STALE " + stateCount("STALE") + " RESEARCH " + stateCount("RESEARCH")
                + " · tracked " + candidates.activeOrderCount() + " · API " + auctions.status().state() + "/" + auctions.status().flipCount()), left, top + 26, 0xAAAAAA);
        if (orderExecutor.status().phase() != OrderCreationExecutor.Phase.IDLE) {
            context.drawTextWithShadow(textRenderer, Text.literal("Executor: " + orderExecutor.status().phase() + " · " + orderExecutor.status().message()), left, top + 176, 0xFFCC66);
        }
        context.drawTextWithShadow(textRenderer, Text.literal("Order plan · " + portfolio.selections().size() + "/" + portfolio.availableOrderSlots()
                + " free offer slots · escrow $" + FlipNotifier.format(portfolio.selectedCapital()) + " · "
                + compact(portfolio.totalExitBatches()) + " future listings · page " + (portfolioPage + 1) + "/"
                + Math.max(1, (portfolio.selections().size() + PAGE_SIZE - 1) / PAGE_SIZE)), left, top + 66, 0xFFFFFF);
        context.drawTextWithShadow(textRenderer, Text.literal("One row = one buy order. Exit stacks are ≤64 and reuse your "
                + portfolio.availableAuctionSlots() + " currently free auction slots."), left, top + 78, 0xAAAAAA);
        if (portfolio.selections().isEmpty()) context.drawTextWithShadow(textRenderer, Text.literal("No current CORE/FILLER offer fits. Collector freshness and local cash are required."), left, top + 104, 0x888888);
        super.render(context, mouseX, mouseY, delta);
    }

    @Override public void close() { client.setScreen(parent); }
    @Override public boolean shouldPause() { return false; }

    private String feedKey() {
        StringBuilder key = new StringBuilder().append(candidates.status().version()).append(':').append(candidates.balance()).append(':')
                .append(candidates.usedOrderSlots()).append(':').append(candidates.usedAuctionSlots()).append(':').append(auctions.status().version())
                .append(':').append(orderExecutor.status().phase()).append(':').append(orderExecutor.status().message());
        for (PortfolioAllocator.Selection selection : candidates.allocation().selections()) key.append(':').append(selection.candidate().id()).append('=').append(selection.batches());
        return key.toString();
    }
    private long stateCount(String state) { return candidates.candidates().stream().filter(candidate -> state.equals(candidate.state())).count(); }
    private long readyCount(boolean filler) { return candidates.candidates().stream().filter(candidate -> "READY".equals(candidate.state()) && isFiller(candidate) == filler).count(); }
    private static boolean isFiller(CandidateFeedClient.Candidate candidate) { return "READY".equals(candidate.state()) && !"actionable".equals(candidate.orderTier()); }
    private static String abbreviate(String value, int limit) { return value.length() <= limit ? value : value.substring(0, limit - 1) + "…"; }
    private static String compact(long value) { return value >= 1_000_000 ? FlipNotifier.format(value) : String.format(java.util.Locale.ROOT, "%,d", value); }
    private static String formatCents(long cents) { return String.format(java.util.Locale.ROOT, "$%,d.%02d", cents / 100, cents % 100); }
}

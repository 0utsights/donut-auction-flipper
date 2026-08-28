package net.donutnetwork.client;

import net.minecraft.client.gui.DrawContext;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.text.Text;

import java.util.List;
import java.util.Locale;

/** Focused player dashboard: portfolio first, controls second, debug detail elsewhere. */
final class DonutScreen extends Screen {
    private static final int WIDTH = 560;
    private static final int HEIGHT = 356;
    private static final int PAGE_SIZE = 4;
    private final Screen parent;
    private final FlipFeedClient auctions;
    private final CandidateFeedClient candidates;
    private final OrderCreationExecutor orderExecutor;
    private String renderedKey = "";
    private int portfolioPage;

    DonutScreen(Screen parent, FlipFeedClient auctions, CandidateFeedClient candidates, OrderCreationExecutor orderExecutor) {
        super(Text.literal("Donut market flipper"));
        this.parent = parent;
        this.auctions = auctions;
        this.candidates = candidates;
        this.orderExecutor = orderExecutor;
    }

    @Override protected void init() {
        Layout layout = layout();
        List<PortfolioAllocator.Selection> selected = candidates.allocation().selections();
        int pageCount = pageCount(selected.size());
        portfolioPage = Math.max(0, Math.min(portfolioPage, pageCount - 1));
        int first = portfolioPage * PAGE_SIZE;
        int visibleRows = Math.min(PAGE_SIZE, Math.max(0, selected.size() - first));
        for (int row = 0; row < visibleRows; row++) {
            PortfolioAllocator.Selection selection = selected.get(first + row);
            CandidateFeedClient.Candidate candidate = selection.candidate();
            MarketUi.MarketButton review = MarketUi.button("REVIEW", layout.left + layout.panelWidth - 106,
                    layout.rowsTop + row * 40 + 8, 88, 24, MarketUi.ButtonStyle.SECONDARY,
                    () -> client.setScreen(new OrderArmScreen(this, orderExecutor, selection)),
                    "Review every value and authorize exactly one order for " + candidate.itemName() + ".");
            review.active = "ORDER_TO_AUCTION".equals(candidate.route()) && !orderExecutor.status().active();
            addDrawableChild(review);
        }

        int footer = layout.top + layout.panelHeight - 36;
        if (orderExecutor.autoEnabled()) {
            addDrawableChild(MarketUi.button("STOP AUTO (" + orderExecutor.autoRemaining() + ")", layout.left + 16, footer, 130, 22,
                    MarketUi.ButtonStyle.DANGER,
                    () -> { orderExecutor.disableAuto(client, "automatic order queue stopped by player"); clearAndInit(); },
                    "Stop the automatic queue and cancel any in-progress order wizard."));
        } else {
            addDrawableChild(MarketUi.button("AUTO ORDERS", layout.left + 16, footer, 130, 22,
                    MarketUi.ButtonStyle.PRIMARY,
                    () -> client.setScreen(new AutoOrderConsentScreen(this, auctions, candidates, orderExecutor)),
                    "Open session consent and see any exact readiness blocker."));
        }
        if (orderExecutor.status().active()) {
            addDrawableChild(MarketUi.button("STOP ORDER", layout.left + 152, footer, 92, 22, MarketUi.ButtonStyle.DANGER,
                    () -> { orderExecutor.cancel(client, "cancelled by player"); clearAndInit(); }, "Stop the current order workflow."));
        } else {
            addDrawableChild(MarketUi.button("LOCAL STATE", layout.left + 152, footer, 92, 22, MarketUi.ButtonStyle.SECONDARY,
                    () -> client.setScreen(new LocalStateScreen(this, auctions, candidates)), "Balance, slots, alerts, and diagnostics."));
        }
        MarketUi.MarketButton previous = MarketUi.button("‹", layout.left + 250, footer, 34, 22, MarketUi.ButtonStyle.GHOST,
                () -> { portfolioPage--; clearAndInit(); }, "Previous portfolio page.");
        previous.active = portfolioPage > 0;
        addDrawableChild(previous);
        MarketUi.MarketButton next = MarketUi.button("›", layout.left + 288, footer, 34, 22, MarketUi.ButtonStyle.GHOST,
                () -> { portfolioPage++; clearAndInit(); }, "Next portfolio page.");
        next.active = portfolioPage + 1 < pageCount;
        addDrawableChild(next);
        addDrawableChild(MarketUi.button("RECHECK LOCKS", layout.left + 328, footer, 110, 22, MarketUi.ButtonStyle.SECONDARY,
                () -> { candidates.recheckTrackedOrders(); clearAndInit(); },
                "Clear local tracked-item locks after manually reconciling Your Orders. Server duplicate checks still apply."));
        addDrawableChild(MarketUi.button("CLOSE", layout.left + layout.panelWidth - 86, footer, 70, 22,
                MarketUi.ButtonStyle.GHOST, this::close, "Close the dashboard."));
        renderedKey = feedKey();
    }

    @Override public void tick() {
        super.tick();
        if (!renderedKey.equals(feedKey())) clearAndInit();
    }

    @Override public void render(DrawContext context, int mouseX, int mouseY, float delta) {
        Layout layout = layout();
        PortfolioAllocator.Allocation portfolio = candidates.allocation();
        List<PortfolioAllocator.Selection> selected = portfolio.selections();
        int pageCount = pageCount(selected.size());
        String connection = "ready".equals(candidates.status().state()) ? "LIVE" : candidates.status().state().toUpperCase(Locale.ROOT);
        int connectionColor = "ready".equals(candidates.status().state()) ? MarketUi.GOOD : MarketUi.WARN;

        MarketUi.drawBackdrop(context, width, height);
        context.createNewRootLayer();
        MarketUi.drawPanel(context, layout.left, layout.top, layout.panelWidth, layout.panelHeight,
                "DONUT MARKET", "ORDER CONSOLE", connection, connectionColor);
        drawMetrics(context, layout, portfolio);
        drawPlanSummary(context, layout, portfolio, pageCount);
        drawRows(context, layout, selected);
        drawExecutor(context, layout);
        context.createNewRootLayer();
        super.render(context, mouseX, mouseY, delta);
    }

    private void drawMetrics(DrawContext context, Layout layout, PortfolioAllocator.Allocation portfolio) {
        int gap = 6;
        int cardWidth = (layout.panelWidth - 32 - gap * 3) / 4;
        int top = layout.top + 54;
        String balanceValue = candidates.balanceUsableForOrders() ? "$" + FlipNotifier.format(portfolio.balance()) : "WAITING";
        metric(context, layout.left + 16, top, cardWidth, "BALANCE", balanceValue,
                candidates.balanceUsableForOrders() ? candidates.balanceSource() : "scoreboard not seen",
                candidates.balanceUsableForOrders() ? MarketUi.TEXT : MarketUi.WARN);
        metric(context, layout.left + 16 + (cardWidth + gap), top, cardWidth, "DEPLOYABLE",
                "$" + FlipNotifier.format(portfolio.deployable()), "reserve " + decimalPercent(portfolio.reserveBps()), MarketUi.TEXT);
        metric(context, layout.left + 16 + (cardWidth + gap) * 2, top, cardWidth, "ORDER SLOTS",
                candidates.usedOrderSlots() + " / 20", (20 - candidates.usedOrderSlots()) + " free", MarketUi.TEXT);
        metric(context, layout.left + 16 + (cardWidth + gap) * 3, top, cardWidth + 2, "AUCTION SLOTS",
                candidates.usedAuctionSlots() + " / 18", (18 - candidates.usedAuctionSlots()) + " free", MarketUi.TEXT);
    }

    private void metric(DrawContext context, int left, int top, int width, String label, String value, String detail, int valueColor) {
        MarketUi.drawCard(context, left, top, width, 45);
        context.drawTextWithShadow(textRenderer, Text.literal(label), left + 8, top + 6, MarketUi.MUTED);
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(value, 17)), left + 8, top + 18, valueColor);
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(detail, 18)), left + 8, top + 31, MarketUi.MUTED);
    }

    private void drawPlanSummary(DrawContext context, Layout layout, PortfolioAllocator.Allocation portfolio, int pageCount) {
        int top = layout.top + 105;
        MarketUi.drawCard(context, layout.left + 16, top, layout.panelWidth - 32, 27);
        String leftText = "PORTFOLIO  " + portfolio.selections().size() + " orders  •  $" + FlipNotifier.format(portfolio.selectedCapital())
                + " escrow  •  +$" + FlipNotifier.format(portfolio.riskAdjustedProfitDay()) + "/day";
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(leftText, 67)), layout.left + 26, top + 9, MarketUi.TEXT);
        String page = "PAGE " + (portfolioPage + 1) + "/" + pageCount;
        context.drawTextWithShadow(textRenderer, Text.literal(page), layout.left + layout.panelWidth - 26 - textRenderer.getWidth(page), top + 9, MarketUi.ACCENT_BRIGHT);
    }

    private void drawRows(DrawContext context, Layout layout, List<PortfolioAllocator.Selection> selected) {
        int first = portfolioPage * PAGE_SIZE;
        int visible = Math.min(PAGE_SIZE, Math.max(0, selected.size() - first));
        if (visible == 0) {
            MarketUi.drawCard(context, layout.left + 16, layout.rowsTop, layout.panelWidth - 32, 70);
            context.drawTextWithShadow(textRenderer, Text.literal("NO ORDERS READY"), layout.left + 28, layout.rowsTop + 15, MarketUi.WARN);
            context.drawTextWithShadow(textRenderer, Text.literal(emptyReason()), layout.left + 28, layout.rowsTop + 32, MarketUi.MUTED);
            context.drawTextWithShadow(textRenderer, Text.literal("Open AUTO ORDERS for the exact blocker or LOCAL STATE for client inputs."),
                    layout.left + 28, layout.rowsTop + 48, MarketUi.MUTED);
            return;
        }
        for (int row = 0; row < visible; row++) {
            PortfolioAllocator.Selection selection = selected.get(first + row);
            CandidateFeedClient.Candidate candidate = selection.candidate();
            int rowTop = layout.rowsTop + row * 40;
            MarketUi.drawCard(context, layout.left + 16, rowTop, layout.panelWidth - 32, 36);
            String tier = isFiller(candidate) ? "FILL" : "CORE";
            int tierColor = isFiller(candidate) ? MarketUi.WARN : MarketUi.GOOD;
            context.drawTextWithShadow(textRenderer, Text.literal(tier), layout.left + 26, rowTop + 7, tierColor);
            context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(candidate.itemName(), 28)), layout.left + 60, rowTop + 7, MarketUi.TEXT);
            String quantity = compact(selection.orderQuantity()) + " units  •  " + compact(selection.batches()) + " × " + candidate.quantity() + " exits";
            context.drawTextWithShadow(textRenderer, Text.literal(quantity), layout.left + 26, rowTop + 21, MarketUi.MUTED);
            String economics = "$" + FlipNotifier.format(selection.capital()) + "  →  +$" + FlipNotifier.format(selection.conservativeProfit());
            int economicsX = layout.left + layout.panelWidth - 116 - textRenderer.getWidth(economics);
            context.drawTextWithShadow(textRenderer, Text.literal(economics), Math.max(layout.left + 250, economicsX), rowTop + 21, MarketUi.GOOD);
        }
    }

    private void drawExecutor(DrawContext context, Layout layout) {
        int top = layout.top + layout.panelHeight - 57;
        OrderCreationExecutor.Status status = orderExecutor.status();
        OrderCreationExecutor.ArmResult readiness = orderExecutor.autoReadiness(client);
        String text;
        int color;
        if (status.active() || status.phase() == OrderCreationExecutor.Phase.ABORTED) {
            text = status.phase() + "  •  " + status.message();
            color = status.phase() == OrderCreationExecutor.Phase.ABORTED ? MarketUi.BAD : MarketUi.WARN;
        } else if (readiness.armed()) {
            text = "READY  •  " + readiness.message();
            color = MarketUi.GOOD;
        } else {
            text = "AUTO BLOCKED  •  " + readiness.message();
            color = MarketUi.WARN;
        }
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(text, 82)), layout.left + 18, top, color);
    }

    private String emptyReason() {
        if (!candidates.balanceUsableForOrders()) return "Waiting for a live scoreboard balance or manual override.";
        if (!"ready".equals(candidates.status().state())) return "Candidate feed is " + candidates.status().state() + ": " + candidates.status().message();
        if (candidates.allocation().availableOrderSlots() < 1) return "All local order slots are marked as used.";
        return "No current candidate clears the local balance, reserve, profit, and evidence gates.";
    }

    private Layout layout() {
        int panelWidth = MarketUi.panelWidth(width, WIDTH);
        int panelHeight = MarketUi.panelHeight(height, HEIGHT);
        int left = MarketUi.panelLeft(width, panelWidth);
        int top = MarketUi.panelTop(height, panelHeight);
        return new Layout(left, top, panelWidth, panelHeight, top + 138);
    }

    private String feedKey() {
        StringBuilder key = new StringBuilder().append(candidates.status().version()).append(':').append(candidates.balance()).append(':')
                .append(candidates.balanceSource()).append(':').append(candidates.usedOrderSlots()).append(':').append(candidates.usedAuctionSlots()).append(':')
                .append(auctions.status().version()).append(':').append(orderExecutor.status().phase()).append(':')
                .append(orderExecutor.status().message()).append(':').append(orderExecutor.autoEnabled()).append(':').append(orderExecutor.autoRemaining());
        for (PortfolioAllocator.Selection selection : candidates.allocation().selections()) key.append(':').append(selection.candidate().id()).append('=').append(selection.batches());
        return key.toString();
    }

    private static boolean isFiller(CandidateFeedClient.Candidate candidate) { return "READY".equals(candidate.state()) && !"actionable".equals(candidate.orderTier()); }
    private static int pageCount(int size) { return Math.max(1, (size + PAGE_SIZE - 1) / PAGE_SIZE); }
    private static String compact(long value) { return value >= 1_000_000 ? FlipNotifier.format(value) : String.format(Locale.ROOT, "%,d", value); }
    private static String decimalPercent(int bps) { return String.format(Locale.ROOT, "%.1f%%", bps / 100.0); }

    @Override public void close() { client.setScreen(parent); }
    @Override public boolean shouldPause() { return false; }

    private record Layout(int left, int top, int panelWidth, int panelHeight, int rowsTop) { }
}

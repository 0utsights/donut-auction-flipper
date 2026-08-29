package net.donutnetwork.client;

import net.minecraft.client.gui.DrawContext;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.text.Text;

/** Explicit local planning controls kept off the primary market dashboard. */
final class LocalStateScreen extends Screen {
    private static final int WIDTH = 440;
    private static final int HEIGHT = 286;
    private final Screen parent;
    private final FlipFeedClient auctions;
    private final CandidateFeedClient candidates;

    LocalStateScreen(Screen parent, FlipFeedClient auctions, CandidateFeedClient candidates) {
        super(Text.literal("Local market settings"));
        this.parent = parent;
        this.auctions = auctions;
        this.candidates = candidates;
    }

    @Override protected void init() {
        int panelWidth = MarketUi.panelWidth(width, WIDTH);
        int panelHeight = MarketUi.panelHeight(height, HEIGHT);
        int left = MarketUi.panelLeft(width, panelWidth);
        int top = MarketUi.panelTop(height, panelHeight);
        int contentLeft = left + 16;
        int rightButton = left + panelWidth - 58;

        addDrawableChild(MarketUi.button("-$10M", rightButton - 156, top + 72, 48, 20, MarketUi.ButtonStyle.GHOST,
                () -> { candidates.adjustBalance(-10_000_000); clearAndInit(); }, "Reduce only the local planning balance by $10 million."));
        addDrawableChild(MarketUi.button("-$1M", rightButton - 104, top + 72, 48, 20, MarketUi.ButtonStyle.GHOST,
                () -> { candidates.adjustBalance(-1_000_000); clearAndInit(); }, "Reduce only the local planning balance by $1 million."));
        addDrawableChild(MarketUi.button("+$1M", rightButton - 52, top + 72, 48, 20, MarketUi.ButtonStyle.SECONDARY,
                () -> { candidates.adjustBalance(1_000_000); clearAndInit(); }, "Promote the saved balance to a manual value and add $1 million."));
        addDrawableChild(MarketUi.button("+$10M", rightButton, top + 72, 50, 20, MarketUi.ButtonStyle.SECONDARY,
                () -> { candidates.adjustBalance(10_000_000); clearAndInit(); }, "Promote the saved balance to a manual value and add $10 million."));
        addDrawableChild(MarketUi.button("-", rightButton - 54, top + 108, 50, 20, MarketUi.ButtonStyle.GHOST,
                () -> { candidates.adjustUsedSlots(-1, 0); clearAndInit(); }, "Mark one fewer order slot as used."));
        addDrawableChild(MarketUi.button("+", rightButton, top + 108, 50, 20, MarketUi.ButtonStyle.SECONDARY,
                () -> { candidates.adjustUsedSlots(1, 0); clearAndInit(); }, "Mark one more order slot as used."));
        addDrawableChild(MarketUi.button("-", rightButton - 54, top + 144, 50, 20, MarketUi.ButtonStyle.GHOST,
                () -> { candidates.adjustUsedSlots(0, -1); clearAndInit(); }, "Mark one fewer auction slot as used."));
        addDrawableChild(MarketUi.button("+", rightButton, top + 144, 50, 20, MarketUi.ButtonStyle.SECONDARY,
                () -> { candidates.adjustUsedSlots(0, 1); clearAndInit(); }, "Mark one more auction slot as used."));
        addDrawableChild(MarketUi.button("Alerts " + (auctions.alertsEnabled() ? "ON" : "OFF"), contentLeft, top + 188, 126, 22,
                auctions.alertsEnabled() ? MarketUi.ButtonStyle.PRIMARY : MarketUi.ButtonStyle.SECONDARY,
                () -> { auctions.setAlertsEnabled(!auctions.alertsEnabled()); clearAndInit(); }, "Toggle API auction alerts in chat."));
        addDrawableChild(MarketUi.button("Diagnostics " + (candidates.diagnosticsEnabled() ? "ON" : "OFF"), contentLeft + 134, top + 188, 136, 22,
                candidates.diagnosticsEnabled() ? MarketUi.ButtonStyle.PRIMARY : MarketUi.ButtonStyle.SECONDARY,
                () -> { candidates.setDiagnostics(!candidates.diagnosticsEnabled()); clearAndInit(); }, "Toggle sanitized diagnostics."));
        addDrawableChild(MarketUi.button("RECHECK LOCKS", contentLeft + 278, top + 188, 114, 22, MarketUi.ButtonStyle.SECONDARY,
                () -> { candidates.recheckTrackedOrders(); clearAndInit(); },
                "Clear candidate locks only after manually reconciling Your Orders."));
        addDrawableChild(MarketUi.button("BACK", left + panelWidth - 116, top + panelHeight - 36, 100, 22,
                MarketUi.ButtonStyle.SECONDARY, this::close, "Return to the market dashboard."));
    }

    @Override public void render(DrawContext context, int mouseX, int mouseY, float delta) {
        int panelWidth = MarketUi.panelWidth(width, WIDTH);
        int panelHeight = MarketUi.panelHeight(height, HEIGHT);
        int left = MarketUi.panelLeft(width, panelWidth);
        int top = MarketUi.panelTop(height, panelHeight);
        MarketUi.drawBackdrop(context, width, height);
        context.createNewRootLayer();
        MarketUi.drawPanel(context, left, top, panelWidth, panelHeight, "DONUT MARKET", "LOCAL STATE", "CLIENT ONLY", MarketUi.MUTED);
        int cardLeft = left + 16;
        int cardWidth = panelWidth - 32;
        for (int row = 0; row < 3; row++) MarketUi.drawCard(context, cardLeft, top + 64 + row * 36, cardWidth, 28);
        context.drawTextWithShadow(textRenderer, Text.literal("Balance"), cardLeft + 10, top + 74, MarketUi.TEXT);
        context.drawTextWithShadow(textRenderer, Text.literal("$" + FlipNotifier.format(candidates.balance()) + " · " + candidates.balanceSource()), cardLeft + 80, top + 74,
                candidates.balanceUsableForOrders() ? MarketUi.GOOD : MarketUi.WARN);
        context.drawTextWithShadow(textRenderer, Text.literal("Order slots"), cardLeft + 10, top + 110, MarketUi.TEXT);
        context.drawTextWithShadow(textRenderer, Text.literal(candidates.usedOrderSlots() + " used · " + (20 - candidates.usedOrderSlots()) + " free"), cardLeft + 80, top + 110, MarketUi.MUTED);
        context.drawTextWithShadow(textRenderer, Text.literal("Auction slots"), cardLeft + 10, top + 146, MarketUi.TEXT);
        context.drawTextWithShadow(textRenderer, Text.literal(candidates.usedAuctionSlots() + " used · " + (18 - candidates.usedAuctionSlots()) + " free"), cardLeft + 80, top + 146, MarketUi.MUTED);
        context.drawWrappedTextWithShadow(textRenderer, Text.literal("The scoreboard normally owns balance. Manual changes affect planning only and never change server currency."),
                cardLeft, top + 222, cardWidth - 118, MarketUi.MUTED);
        context.createNewRootLayer();
        super.render(context, mouseX, mouseY, delta);
    }

    @Override public void close() { client.setScreen(parent); }
    @Override public boolean shouldPause() { return false; }
}

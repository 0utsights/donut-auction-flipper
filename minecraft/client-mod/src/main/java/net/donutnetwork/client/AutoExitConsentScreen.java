package net.donutnetwork.client;

import net.minecraft.client.gui.DrawContext;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.text.Text;

/** Explicit session consent for all economic actions in the auction exit workflow. */
final class AutoExitConsentScreen extends Screen {
    private static final int WIDTH = 500;
    private static final int HEIGHT = 318;
    private final Screen parent;
    private final CandidateFeedClient candidates;
    private final AuctionExitExecutor executor;
    private String validation = "";

    AutoExitConsentScreen(Screen parent, CandidateFeedClient candidates, AuctionExitExecutor executor) {
        super(Text.literal("Enable automatic auction exits"));
        this.parent = parent;
        this.candidates = candidates;
        this.executor = executor;
    }

    @Override protected void init() {
        int panelWidth = MarketUi.panelWidth(width, WIDTH);
        int panelHeight = MarketUi.panelHeight(height, HEIGHT);
        int left = MarketUi.panelLeft(width, panelWidth);
        int top = MarketUi.panelTop(height, panelHeight);
        int footer = top + panelHeight - 38;
        boolean allowed = executor.canEnable(client);
        addDrawableChild(MarketUi.button(allowed ? "ENABLE SESSION" : "CHECK SERVER", left + 16, footer, 176, 24,
                allowed ? MarketUi.ButtonStyle.PRIMARY : MarketUi.ButtonStyle.SECONDARY,
                () -> {
                    if (!executor.canEnable(client)) {
                        validation = executor.readiness(client);
                        return;
                    }
                    executor.enable(client);
                    client.setScreen(null);
                }, executor.readiness(client)));
        addDrawableChild(MarketUi.button("CANCEL", left + panelWidth - 100, footer, 84, 24,
                MarketUi.ButtonStyle.GHOST, this::close, "Return without authorizing any action."));
    }

    @Override public void render(DrawContext context, int mouseX, int mouseY, float delta) {
        int panelWidth = MarketUi.panelWidth(width, WIDTH);
        int panelHeight = MarketUi.panelHeight(height, HEIGHT);
        int left = MarketUi.panelLeft(width, panelWidth);
        int top = MarketUi.panelTop(height, panelHeight);
        boolean allowed = executor.canEnable(client);

        MarketUi.drawBackdrop(context, width, height);
        context.createNewRootLayer();
        MarketUi.drawPanel(context, left, top, panelWidth, panelHeight, "DONUT MARKET", "AUTOMATIC EXITS", "SESSION ONLY", MarketUi.ACCENT_BRIGHT);
        MarketUi.drawCard(context, left + 16, top + 54, panelWidth - 32, 42);
        context.drawTextWithShadow(textRenderer, Text.literal(allowed ? "READY TO AUTHORIZE" : "SERVER CHECK FAILED"),
                left + 28, top + 65, allowed ? MarketUi.GOOD : MarketUi.BAD);
        String readiness = validation.isBlank() ? executor.readiness(client) : validation;
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(readiness, 69)), left + 28, top + 80, MarketUi.MUTED);

        context.drawTextWithShadow(textRenderer, Text.literal("THIS SESSION MAY"), left + 18, top + 112, MarketUi.MUTED);
        row(context, left, top + 132, "1", "Collect only the exact completed quantity from a tracked order.");
        row(context, left, top + 158, "2", "Buy verified empty shulkers when the exit occupies 27+ inventory slots.");
        row(context, left, top + 184, "3", "Place, pack, recover, and list exact contents at the planned price.");
        row(context, left, top + 210, "4", "Confirm a purchase or listing only when item, quantity, and price match.");

        MarketUi.drawCard(context, left + 16, top + 239, panelWidth - 32, 36);
        String counts = candidates.exitReadyCount() + " exits ready  •  " + (18 - candidates.usedAuctionSlots()) + " auction slots free";
        context.drawTextWithShadow(textRenderer, Text.literal(counts), left + 28, top + 249, MarketUi.TEXT);
        context.drawTextWithShadow(textRenderer, Text.literal("If none are ready, the session stays idle until one completes."), left + 28, top + 263, MarketUi.MUTED);
        context.createNewRootLayer();
        super.render(context, mouseX, mouseY, delta);
    }

    private void row(DrawContext context, int left, int top, String number, String text) {
        MarketUi.drawCard(context, left + 16, top, 26, 21);
        context.drawTextWithShadow(textRenderer, Text.literal(number), left + 26, top + 7, MarketUi.ACCENT_BRIGHT);
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(text, 70)), left + 52, top + 7, MarketUi.TEXT);
    }

    @Override public void close() { client.setScreen(parent); }
    @Override public boolean shouldPause() { return false; }
}

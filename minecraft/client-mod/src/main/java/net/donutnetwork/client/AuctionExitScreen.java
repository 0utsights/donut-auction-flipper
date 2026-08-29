package net.donutnetwork.client;

import net.minecraft.client.gui.DrawContext;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.text.Text;

import java.util.List;
import java.util.Locale;

/** Compact view of durable order inventory and its exact auction exit plan. */
final class AuctionExitScreen extends Screen {
    private static final int WIDTH = 540;
    private static final int HEIGHT = 338;
    private static final int PAGE_SIZE = 4;
    private final Screen parent;
    private final CandidateFeedClient candidates;
    private final AuctionExitExecutor executor;
    private int page;
    private String renderedKey = "";

    AuctionExitScreen(Screen parent, CandidateFeedClient candidates, AuctionExitExecutor executor) {
        super(Text.literal("Tracked order exits"));
        this.parent = parent;
        this.candidates = candidates;
        this.executor = executor;
    }

    @Override protected void init() {
        int panelWidth = MarketUi.panelWidth(width, WIDTH);
        int panelHeight = MarketUi.panelHeight(height, HEIGHT);
        int left = MarketUi.panelLeft(width, panelWidth);
        int top = MarketUi.panelTop(height, panelHeight);
        int pages = pages(candidates.orderPositions().size());
        page = Math.max(0, Math.min(page, pages - 1));
        int footer = top + panelHeight - 37;

        if (executor.enabled()) {
            addDrawableChild(MarketUi.button("STOP SESSION", left + 16, footer, 132, 23, MarketUi.ButtonStyle.DANGER,
                    () -> { executor.disable(client, "automatic exit session stopped by player"); clearAndInit(); },
                    "Stop the session. Any partially completed workflow is held for review."));
        } else {
            addDrawableChild(MarketUi.button("AUTO EXITS", left + 16, footer, 132, 23, MarketUi.ButtonStyle.PRIMARY,
                    () -> client.setScreen(new AutoExitConsentScreen(this, candidates, executor)),
                    "Review and authorize the session's purchase, claim, packaging, and listing actions."));
        }
        MarketUi.MarketButton previous = MarketUi.button("‹", left + 156, footer, 34, 23, MarketUi.ButtonStyle.GHOST,
                () -> { page--; clearAndInit(); }, "Previous tracked-position page.");
        previous.active = page > 0;
        addDrawableChild(previous);
        MarketUi.MarketButton next = MarketUi.button("›", left + 194, footer, 34, 23, MarketUi.ButtonStyle.GHOST,
                () -> { page++; clearAndInit(); }, "Next tracked-position page.");
        next.active = page + 1 < pages;
        addDrawableChild(next);
        addDrawableChild(MarketUi.button("BACK", left + panelWidth - 92, footer, 76, 23,
                MarketUi.ButtonStyle.SECONDARY, this::close, "Return to the order dashboard."));
        renderedKey = stateKey();
    }

    @Override public void tick() {
        super.tick();
        if (!renderedKey.equals(stateKey())) clearAndInit();
    }

    @Override public void render(DrawContext context, int mouseX, int mouseY, float delta) {
        int panelWidth = MarketUi.panelWidth(width, WIDTH);
        int panelHeight = MarketUi.panelHeight(height, HEIGHT);
        int left = MarketUi.panelLeft(width, panelWidth);
        int top = MarketUi.panelTop(height, panelHeight);
        List<LocalOrderPosition> positions = candidates.orderPositions();
        int pages = pages(positions.size());
        AuctionExitExecutor.Status status = executor.status();

        MarketUi.drawBackdrop(context, width, height);
        context.createNewRootLayer();
        MarketUi.drawPanel(context, left, top, panelWidth, panelHeight, "DONUT MARKET", "AUCTION EXITS",
                executor.enabled() ? "SESSION ON" : "SESSION OFF", executor.enabled() ? MarketUi.GOOD : MarketUi.MUTED);

        MarketUi.drawCard(context, left + 16, top + 54, panelWidth - 32, 38);
        int statusColor = status.phase() == AuctionExitExecutor.Phase.ABORTED ? MarketUi.BAD
                : status.active() ? MarketUi.WARN : executor.enabled() ? MarketUi.GOOD : MarketUi.MUTED;
        context.drawTextWithShadow(textRenderer, Text.literal(status.phase().name().replace('_', ' ')), left + 27, top + 64, statusColor);
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(status.message(), 72)), left + 27, top + 78, MarketUi.MUTED);

        context.drawTextWithShadow(textRenderer, Text.literal("TRACKED POSITIONS  •  PAGE " + (page + 1) + "/" + pages),
                left + 18, top + 103, MarketUi.MUTED);
        int first = page * PAGE_SIZE;
        int visible = Math.min(PAGE_SIZE, Math.max(0, positions.size() - first));
        if (visible == 0) {
            MarketUi.drawCard(context, left + 16, top + 120, panelWidth - 32, 64);
            context.drawTextWithShadow(textRenderer, Text.literal("NO DURABLE ORDER POSITIONS"), left + 28, top + 135, MarketUi.WARN);
            String detail = candidates.activeOrderCount() > 0
                    ? candidates.activeOrderCount() + " pre-alpha28 order lock(s) have no frozen exit economics and require manual reconciliation."
                    : "You can enable the session now; it will wait for a tracked order to complete.";
            context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(detail, 75)),
                    left + 28, top + 153, MarketUi.MUTED);
        }
        for (int row = 0; row < visible; row++) {
            LocalOrderPosition position = positions.get(first + row);
            int rowTop = top + 120 + row * 41;
            MarketUi.drawCard(context, left + 16, rowTop, panelWidth - 32, 37);
            AuctionExitPlan plan = plan(position);
            int stateColor = position.state() == LocalOrderPosition.State.HOLD ? MarketUi.BAD
                    : position.deliveredQuantity() == position.totalQuantity() ? MarketUi.GOOD : MarketUi.WARN;
            context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(position.itemName(), 27)), left + 27, rowTop + 7, MarketUi.TEXT);
            context.drawTextWithShadow(textRenderer, Text.literal(position.state().name()), left + 196, rowTop + 7, stateColor);
            String mode = plan == null ? "WAITING" : plan.mode() + "  •  " + plan.listings().size() + " listing" + (plan.listings().size() == 1 ? "" : "s");
            context.drawTextWithShadow(textRenderer, Text.literal(mode), left + 325, rowTop + 7, plan == null ? MarketUi.MUTED : MarketUi.ACCENT_BRIGHT);
            String progress = "delivered " + quantity(position.deliveredQuantity()) + "/" + quantity(position.totalQuantity())
                    + "  •  claimed " + quantity(position.claimedQuantity()) + "  •  listed " + quantity(position.listedQuantity());
            context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(progress, 76)), left + 27, rowTop + 22, MarketUi.MUTED);
        }
        context.createNewRootLayer();
        super.render(context, mouseX, mouseY, delta);
    }

    private static AuctionExitPlan plan(LocalOrderPosition position) {
        try { return AuctionExitPlan.from(position); }
        catch (IllegalArgumentException | ArithmeticException ignored) { return null; }
    }

    private String stateKey() {
        StringBuilder key = new StringBuilder().append(executor.enabled()).append(':').append(executor.status().phase())
                .append(':').append(executor.status().message()).append(':').append(candidates.usedAuctionSlots());
        for (LocalOrderPosition position : candidates.orderPositions()) {
            key.append(':').append(position.itemId()).append('=').append(position.deliveredQuantity()).append('/')
                    .append(position.claimedQuantity()).append('/').append(position.packagedQuantity()).append('/')
                    .append(position.listedQuantity()).append('/').append(position.state());
        }
        return key.toString();
    }

    private static int pages(int count) { return Math.max(1, (count + PAGE_SIZE - 1) / PAGE_SIZE); }
    private static String quantity(int value) { return String.format(Locale.ROOT, "%,d", value); }
    @Override public void close() { client.setScreen(parent); }
    @Override public boolean shouldPause() { return false; }
}

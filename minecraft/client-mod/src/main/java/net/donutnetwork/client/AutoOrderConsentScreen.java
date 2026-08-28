package net.donutnetwork.client;

import net.minecraft.client.gui.DrawContext;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.text.Text;

import java.util.List;

/** Clear session-scoped consent with a visible, actionable readiness explanation. */
final class AutoOrderConsentScreen extends Screen {
    private static final int WIDTH = 500;
    private static final int HEIGHT = 316;
    private final Screen parent;
    private final FlipFeedClient auctions;
    private final CandidateFeedClient candidates;
    private final OrderCreationExecutor executor;
    private String validation = "";
    private String renderedKey = "";

    AutoOrderConsentScreen(Screen parent, FlipFeedClient auctions, CandidateFeedClient candidates, OrderCreationExecutor executor) {
        super(Text.literal("Enable automatic buy orders"));
        this.parent = parent;
        this.auctions = auctions;
        this.candidates = candidates;
        this.executor = executor;
    }

    @Override protected void init() {
        int panelWidth = MarketUi.panelWidth(width, WIDTH);
        int panelHeight = MarketUi.panelHeight(height, HEIGHT);
        int left = MarketUi.panelLeft(width, panelWidth);
        int top = MarketUi.panelTop(height, panelHeight);
        int footer = top + panelHeight - 38;
        OrderCreationExecutor.ArmResult readiness = executor.autoReadiness(client);
        String actionLabel = readiness.armed() ? "ENABLE SESSION" : "CHECK REQUIREMENTS";
        MarketUi.MarketButton enable = MarketUi.button(actionLabel, left + 16, footer, 176, 24,
                readiness.armed() ? MarketUi.ButtonStyle.PRIMARY : MarketUi.ButtonStyle.SECONDARY,
                () -> {
                    OrderCreationExecutor.ArmResult result = executor.enableAuto(client);
                    validation = result.message();
                    if (result.armed()) client.setScreen(null); else clearAndInit();
                }, readiness.message());
        // The blocker button deliberately remains clickable so it explains why
        // consent cannot proceed instead of appearing mysteriously inert.
        addDrawableChild(enable);
        addDrawableChild(MarketUi.button("LOCAL STATE", left + 200, footer, 110, 24, MarketUi.ButtonStyle.SECONDARY,
                () -> client.setScreen(new LocalStateScreen(this, auctions, candidates)),
                "Set a local balance override or reconcile used slots."));
        addDrawableChild(MarketUi.button("CANCEL", left + panelWidth - 100, footer, 84, 24,
                MarketUi.ButtonStyle.GHOST, this::close, "Return without authorizing anything."));
        renderedKey = readinessKey();
    }

    @Override public void tick() {
        super.tick();
        if (!renderedKey.equals(readinessKey())) {
            validation = "";
            clearAndInit();
        }
    }

    @Override public void render(DrawContext context, int mouseX, int mouseY, float delta) {
        int panelWidth = MarketUi.panelWidth(width, WIDTH);
        int panelHeight = MarketUi.panelHeight(height, HEIGHT);
        int left = MarketUi.panelLeft(width, panelWidth);
        int top = MarketUi.panelTop(height, panelHeight);
        PortfolioAllocator.Allocation allocation = candidates.allocation();
        OrderCreationExecutor.ArmResult readiness = executor.autoReadiness(client);
        int stateColor = readiness.armed() ? MarketUi.GOOD : MarketUi.WARN;
        String state = readiness.armed() ? "READY" : "BLOCKED";

        MarketUi.drawBackdrop(context, width, height);
        context.createNewRootLayer();
        MarketUi.drawPanel(context, left, top, panelWidth, panelHeight, "DONUT MARKET", "AUTOMATIC ORDERS", "SESSION ONLY", MarketUi.ACCENT_BRIGHT);
        MarketUi.drawCard(context, left + 16, top + 55, panelWidth - 32, 43);
        context.drawTextWithShadow(textRenderer, Text.literal(state), left + 28, top + 65, stateColor);
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(readiness.message(), 69)), left + 28, top + 80, MarketUi.MUTED);

        int metricTop = top + 106;
        int gap = 6;
        int cardWidth = (panelWidth - 32 - gap * 2) / 3;
        metric(context, left + 16, metricTop, cardWidth, "PLANNED", allocation.selections().size() + " orders");
        metric(context, left + 16 + cardWidth + gap, metricTop, cardWidth, "MAX ESCROW", "$" + FlipNotifier.format(allocation.selectedCapital()));
        metric(context, left + 16 + (cardWidth + gap) * 2, metricTop, cardWidth, "FREE SLOTS", allocation.availableOrderSlots() + " / 20");

        context.drawTextWithShadow(textRenderer, Text.literal("REVIEWED QUEUE"), left + 18, top + 158, MarketUi.MUTED);
        List<PortfolioAllocator.Selection> selections = allocation.selections();
        if (selections.isEmpty()) {
            context.drawTextWithShadow(textRenderer, Text.literal("No eligible item is currently allocated."), left + 28, top + 174, MarketUi.WARN);
        } else {
            for (int index = 0; index < Math.min(3, selections.size()); index++) {
                PortfolioAllocator.Selection selection = selections.get(index);
                String row = (index + 1) + ". " + MarketUi.trim(selection.candidate().itemName(), 28) + "  •  "
                        + String.format(java.util.Locale.ROOT, "%,d", selection.orderQuantity()) + " units  •  $" + FlipNotifier.format(selection.capital());
                context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(row, 70)), left + 28, top + 174 + index * 14, MarketUi.TEXT);
            }
            if (selections.size() > 3) context.drawTextWithShadow(textRenderer, Text.literal("+ " + (selections.size() - 3) + " more reviewed orders"),
                    left + 28, top + 216, MarketUi.MUTED);
        }

        context.drawTextWithShadow(textRenderer, Text.literal("EVERY ORDER"), left + 18, top + 230, MarketUi.MUTED);
        context.drawTextWithShadow(textRenderer, Text.literal("Fresh markets  •  exact menu values  •  duplicate check  •  post-submit proof"),
                left + 28, top + 246, MarketUi.TEXT);
        String note = validation.isBlank() ? "Nothing is authorized until you press Enable Session." : validation;
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(note, 74)), left + 28, top + 261,
                validation.isBlank() ? MarketUi.MUTED : stateColor);
        context.createNewRootLayer();
        super.render(context, mouseX, mouseY, delta);
    }

    private void metric(DrawContext context, int left, int top, int width, String label, String value) {
        MarketUi.drawCard(context, left, top, width, 42);
        context.drawTextWithShadow(textRenderer, Text.literal(label), left + 8, top + 7, MarketUi.MUTED);
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(value, 19)), left + 8, top + 23, MarketUi.TEXT);
    }

    private String readinessKey() {
        PortfolioAllocator.Allocation allocation = candidates.allocation();
        return candidates.status().version() + ":" + candidates.balance() + ":" + candidates.balanceSource() + ":"
                + allocation.availableOrderSlots() + ":" + allocation.selectedCapital() + ":" + allocation.selections().size() + ":"
                + executor.status().phase() + ":" + executor.status().message();
    }

    @Override public void close() { client.setScreen(parent); }
    @Override public boolean shouldPause() { return false; }
}

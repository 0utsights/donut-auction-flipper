package net.donutnetwork.client;

import net.minecraft.client.gui.DrawContext;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.text.Text;

import java.time.Instant;
import java.util.Locale;

/** Local, explicit one-order consent screen. Opening a candidate never arms execution. */
final class OrderArmScreen extends Screen {
    private static final int WIDTH = 460;
    private static final int HEIGHT = 294;
    private final Screen parent;
    private final OrderCreationExecutor executor;
    private final PortfolioAllocator.Selection selection;
    private final CandidateFeedClient.Candidate candidate;
    private String validation = "";

    OrderArmScreen(Screen parent, OrderCreationExecutor executor, PortfolioAllocator.Selection selection) {
        super(Text.literal("Review one Donut order"));
        this.parent = parent;
        this.executor = executor;
        this.selection = selection;
        this.candidate = selection.candidate();
    }

    @Override protected void init() {
        int panelWidth = MarketUi.panelWidth(width, WIDTH);
        int panelHeight = MarketUi.panelHeight(height, HEIGHT);
        int left = MarketUi.panelLeft(width, panelWidth);
        int top = MarketUi.panelTop(height, panelHeight);
        int footer = top + panelHeight - 38;
        OrderCreationExecutor.ArmResult check = executor.canArm(selection, Instant.now());
        validation = check.message();
        MarketUi.MarketButton arm = MarketUi.button(check.armed() ? "ARM ONE ORDER" : "NOT READY", left + 16, footer, 190, 24,
                check.armed() ? MarketUi.ButtonStyle.PRIMARY : MarketUi.ButtonStyle.SECONDARY,
                () -> {
                    OrderCreationExecutor.ArmResult result = executor.arm(selection);
                    validation = result.message();
                    if (result.armed()) client.setScreen(null); else clearAndInit();
                }, check.message());
        arm.active = check.armed();
        addDrawableChild(arm);
        addDrawableChild(MarketUi.button("CANCEL", left + panelWidth - 100, footer, 84, 24,
                MarketUi.ButtonStyle.GHOST, this::close, "Return without authorizing an order."));
    }

    @Override public void render(DrawContext context, int mouseX, int mouseY, float delta) {
        int panelWidth = MarketUi.panelWidth(width, WIDTH);
        int panelHeight = MarketUi.panelHeight(height, HEIGHT);
        int left = MarketUi.panelLeft(width, panelWidth);
        int top = MarketUi.panelTop(height, panelHeight);
        OrderPlan plan = OrderPlan.from(selection);
        boolean core = "actionable".equals(candidate.orderTier());
        boolean ready = validation.startsWith("ready") || validation.startsWith("will");

        MarketUi.drawBackdrop(context, width, height);
        context.createNewRootLayer();
        MarketUi.drawPanel(context, left, top, panelWidth, panelHeight, "DONUT MARKET", "REVIEW ONE ORDER",
                core ? "CORE" : "FILL", core ? MarketUi.GOOD : MarketUi.WARN);
        MarketUi.drawCard(context, left + 16, top + 56, panelWidth - 32, 48);
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(candidate.itemName(), 42)), left + 28, top + 67, MarketUi.TEXT);
        context.drawTextWithShadow(textRenderer, Text.literal(String.format(Locale.ROOT, "%,d units  •  %,d exits × %d", plan.quantity(), plan.batches(), plan.batchQuantity())),
                left + 28, top + 84, MarketUi.MUTED);

        int metricTop = top + 112;
        int gap = 6;
        int cardWidth = (panelWidth - 32 - gap * 2) / 3;
        metric(context, left + 16, metricTop, cardWidth, "UNIT REWARD", "$" + plan.priceInput());
        metric(context, left + 16 + cardWidth + gap, metricTop, cardWidth, "MAX ESCROW", "$" + FlipNotifier.format(plan.escrowDollars()));
        metric(context, left + 16 + (cardWidth + gap) * 2, metricTop, cardWidth, "PROFIT", "+$" + FlipNotifier.format(selection.conservativeProfit()));

        MarketUi.drawCard(context, left + 16, top + 164, panelWidth - 32, 72);
        context.drawTextWithShadow(textRenderer, Text.literal("EXIT PLAN"), left + 28, top + 174, MarketUi.MUTED);
        context.drawTextWithShadow(textRenderer, Text.literal(plan.batches() + " listings of " + plan.batchQuantity() + " at $" + FlipNotifier.format(candidate.targetListPrice())),
                left + 28, top + 190, MarketUi.TEXT);
        context.drawTextWithShadow(textRenderer, Text.literal(core ? "Measured fills support this order size." : "Conservative starter; it may fill slowly."),
                left + 28, top + 205, core ? MarketUi.GOOD : MarketUi.WARN);
        context.drawTextWithShadow(textRenderer, Text.literal("Any changed, stale, duplicate, or unknown value stops execution."), left + 28, top + 220, MarketUi.MUTED);
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(validation, 66)), left + 18, top + 244,
                ready ? MarketUi.GOOD : MarketUi.BAD);
        context.createNewRootLayer();
        super.render(context, mouseX, mouseY, delta);
    }

    private void metric(DrawContext context, int left, int top, int width, String label, String value) {
        MarketUi.drawCard(context, left, top, width, 44);
        context.drawTextWithShadow(textRenderer, Text.literal(label), left + 8, top + 7, MarketUi.MUTED);
        context.drawTextWithShadow(textRenderer, Text.literal(MarketUi.trim(value, 18)), left + 8, top + 24, MarketUi.TEXT);
    }

    @Override public void close() { client.setScreen(parent); }
    @Override public boolean shouldPause() { return false; }
}

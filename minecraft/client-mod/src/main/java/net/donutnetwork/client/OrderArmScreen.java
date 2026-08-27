package net.donutnetwork.client;

import net.minecraft.client.gui.DrawContext;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.client.gui.tooltip.Tooltip;
import net.minecraft.client.gui.widget.ButtonWidget;
import net.minecraft.text.Text;

import java.time.Instant;

/** Local, explicit one-order consent screen. Opening a candidate alone never arms execution. */
final class OrderArmScreen extends Screen {
    private static final int WIDTH = 420;
    private final Screen parent;
    private final OrderCreationExecutor executor;
    private final PortfolioAllocator.Selection selection;
    private final CandidateFeedClient.Candidate candidate;
    private String validation = "";

    OrderArmScreen(Screen parent, OrderCreationExecutor executor, PortfolioAllocator.Selection selection) {
        super(Text.literal("Arm one Donut order"));
        this.parent = parent; this.executor = executor; this.selection = selection; this.candidate = selection.candidate();
    }

    @Override protected void init() {
        int left = (width - WIDTH) / 2;
        int top = Math.max(24, (height - 230) / 2);
        OrderCreationExecutor.ArmResult check = executor.canArm(selection, Instant.now());
        validation = check.message();
        ButtonWidget arm = ButtonWidget.builder(Text.literal("ARM ONE ORDER"), button -> {
            OrderCreationExecutor.ArmResult result = executor.arm(selection);
            validation = result.message();
            if (result.armed()) client.setScreen(null); else clearAndInit();
        }).tooltip(Tooltip.of(Text.literal("Authorizes exactly one Create Order click after live revalidation. It does not loop.")))
                .dimensions(left, top + 170, 204, 20).build();
        arm.active = check.armed();
        addDrawableChild(arm);
        addDrawableChild(ButtonWidget.builder(Text.literal("Cancel"), button -> close()).dimensions(left + 216, top + 170, 204, 20).build());
    }

    @Override public void render(DrawContext context, int mouseX, int mouseY, float delta) {
        context.fill(0, 0, width, height, 0xFF101010);
        int left = (width - WIDTH) / 2;
        int top = Math.max(24, (height - 230) / 2);
        context.drawCenteredTextWithShadow(textRenderer, title, width / 2, top, 0xFFFFFF);
        OrderPlan plan = OrderPlan.from(selection);
        context.drawTextWithShadow(textRenderer, Text.literal(plan.quantity() + "× " + candidate.itemName() + " in one order (" + plan.batches() + " stacks)"), left, top + 30, 0xFFFFFF);
        context.drawTextWithShadow(textRenderer, Text.literal("Order reward: $" + plan.priceInput() + " per item"), left, top + 48, 0xDDDDDD);
        context.drawTextWithShadow(textRenderer, Text.literal("Maximum escrow: $" + FlipNotifier.format(plan.escrowDollars())), left, top + 64, 0xDDDDDD);
        context.drawTextWithShadow(textRenderer, Text.literal("Relist: " + plan.batches() + " × " + plan.batchQuantity() + " at $" + FlipNotifier.format(candidate.targetListPrice())), left, top + 80, 0xDDDDDD);
        context.drawTextWithShadow(textRenderer, Text.literal("Conservative total profit: +$" + FlipNotifier.format(selection.conservativeProfit())), left, top + 96, 0xDDDDDD);
        context.drawTextWithShadow(textRenderer, Text.literal("Session budget remaining: $" + FlipNotifier.format(executor.status().sessionBudget() - executor.status().sessionSpent())), left, top + 118, 0xBBBBBB);
        context.drawTextWithShadow(textRenderer, Text.literal("This arm expires on any changed, stale, or unknown screen/value."), left, top + 138, 0xAAAAAA);
        context.drawTextWithShadow(textRenderer, Text.literal(validation), left, top + 152, validation.startsWith("ready") || validation.startsWith("will") ? 0x66DD88 : 0xFF7777);
        super.render(context, mouseX, mouseY, delta);
    }

    @Override public void close() { client.setScreen(parent); }
    @Override public boolean shouldPause() { return false; }
}

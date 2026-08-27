package net.donutnetwork.client;

import net.minecraft.client.gui.DrawContext;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.client.gui.tooltip.Tooltip;
import net.minecraft.client.gui.widget.ButtonWidget;
import net.minecraft.text.Text;

/** Explicit session-scoped consent for sequential economic actions. */
final class AutoOrderConsentScreen extends Screen {
    private static final int WIDTH = 430;
    private final Screen parent;
    private final CandidateFeedClient candidates;
    private final OrderCreationExecutor executor;
    private String validation = "";

    AutoOrderConsentScreen(Screen parent, CandidateFeedClient candidates, OrderCreationExecutor executor) {
        super(Text.literal("Enable automatic buy orders"));
        this.parent = parent;
        this.candidates = candidates;
        this.executor = executor;
    }

    @Override protected void init() {
        int left = (width - WIDTH) / 2;
        int top = Math.max(24, (height - 230) / 2);
        ButtonWidget enable = ButtonWidget.builder(Text.literal("ENABLE AUTO ORDERS"), button -> {
            OrderCreationExecutor.ArmResult result = executor.enableAuto(client);
            validation = result.message();
            if (result.armed()) client.setScreen(null); else clearAndInit();
        }).tooltip(Tooltip.of(Text.literal("Allows sequential Create Order clicks for this Minecraft session. Any failed verification stops the queue.")))
                .dimensions(left, top + 184, 210, 20).build();
        enable.active = !executor.status().active() && candidates.balanceUsableForOrders()
                && candidates.allocation().availableOrderSlots() > 0;
        addDrawableChild(enable);
        addDrawableChild(ButtonWidget.builder(Text.literal("Cancel"), button -> close())
                .dimensions(left + 220, top + 184, 210, 20).build());
    }

    @Override public void render(DrawContext context, int mouseX, int mouseY, float delta) {
        context.fill(0, 0, width, height, 0xFF101010);
        int left = (width - WIDTH) / 2;
        int top = Math.max(24, (height - 230) / 2);
        PortfolioAllocator.Allocation allocation = candidates.allocation();
        context.drawCenteredTextWithShadow(textRenderer, title, width / 2, top, 0xFFFFFF);
        context.drawTextWithShadow(textRenderer, Text.literal("This authorizes real in-game buy-order creation."), left, top + 30, 0xFFCC66);
        context.drawTextWithShadow(textRenderer, Text.literal("Planned orders: " + allocation.selections().size() + " · available slots: "
                + allocation.availableOrderSlots()), left, top + 50, 0xDDDDDD);
        context.drawTextWithShadow(textRenderer, Text.literal("Live balance: $" + FlipNotifier.format(allocation.balance()) + " ("
                + candidates.balanceSource() + ")"), left, top + 66, 0xDDDDDD);
        context.drawTextWithShadow(textRenderer, Text.literal("Deployable after reserve: $" + FlipNotifier.format(allocation.deployable())), left, top + 82, 0xDDDDDD);
        context.drawTextWithShadow(textRenderer, Text.literal("Maximum currently planned escrow: $" + FlipNotifier.format(allocation.selectedCapital())), left, top + 98, 0xDDDDDD);
        context.drawTextWithShadow(textRenderer, Text.literal("For every item Fabric will:"), left, top + 120, 0xFFFFFF);
        context.drawTextWithShadow(textRenderer, Text.literal("• refresh both markets and reject changed economics"), left + 8, top + 136, 0xAAAAAA);
        context.drawTextWithShadow(textRenderer, Text.literal("• verify every menu, item, quantity, price, and total"), left + 8, top + 150, 0xAAAAAA);
        context.drawTextWithShadow(textRenderer, Text.literal("• prove the order appears in Your Orders before continuing"), left + 8, top + 164, 0xAAAAAA);
        if (!validation.isBlank()) context.drawTextWithShadow(textRenderer, Text.literal(validation), left, top + 174, 0xFF7777);
        super.render(context, mouseX, mouseY, delta);
    }

    @Override public void close() { client.setScreen(parent); }
    @Override public boolean shouldPause() { return false; }
}

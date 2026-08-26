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
    private final Screen parent;
    private final FlipFeedClient auctions;
    private final CandidateFeedClient candidates;
    private final OrderCreationExecutor orderExecutor;
    private String renderedKey = "";

    DonutScreen(Screen parent, FlipFeedClient auctions, CandidateFeedClient candidates, OrderCreationExecutor orderExecutor) {
        super(Text.literal("Donut market flipper")); this.parent = parent; this.auctions = auctions; this.candidates = candidates; this.orderExecutor = orderExecutor;
    }

    @Override protected void init() {
        int left = (width - WIDTH) / 2;
        int top = Math.max(12, (height - 350) / 2);
        addDrawableChild(ButtonWidget.builder(Text.literal("Balance -$1m"), button -> { candidates.adjustBalance(-1_000_000); clearAndInit(); })
                .dimensions(left, top + 46, 106, 20).build());
        addDrawableChild(ButtonWidget.builder(Text.literal("Balance +$1m"), button -> { candidates.adjustBalance(1_000_000); clearAndInit(); })
                .dimensions(left + 110, top + 46, 106, 20).build());
        addDrawableChild(ButtonWidget.builder(Text.literal("Orders " + candidates.usedOrderSlots() + "/20"), button -> { candidates.adjustUsedSlots(candidates.usedOrderSlots() == 20 ? -20 : 1, 0); clearAndInit(); })
                .tooltip(Tooltip.of(Text.literal("Click to increment used order slots; after 20 it wraps to 0."))).dimensions(left + 220, top + 46, 106, 20).build());
        addDrawableChild(ButtonWidget.builder(Text.literal("Auctions " + candidates.usedAuctionSlots() + "/18"), button -> { candidates.adjustUsedSlots(0, candidates.usedAuctionSlots() == 18 ? -18 : 1); clearAndInit(); })
                .tooltip(Tooltip.of(Text.literal("Click to increment used auction slots; after 18 it wraps to 0."))).dimensions(left + 330, top + 46, 110, 20).build());

        List<PortfolioAllocator.Selection> selected = candidates.allocation().selections();
        for (int index = 0; index < Math.min(5, selected.size()); index++) {
            PortfolioAllocator.Selection selection = selected.get(index);
            CandidateFeedClient.Candidate candidate = selection.candidate();
            String label = selection.batches() + " batch · " + abbreviate(candidate.itemName(), 22) + "  +$"
                    + FlipNotifier.format(candidate.conservativeProfit()) + "  $" + FlipNotifier.format(candidate.riskAdjustedProfitDay()) + "/day";
            addDrawableChild(ButtonWidget.builder(Text.literal(label), button -> CandidateNotifier.open(client, candidates, candidate))
                    .tooltip(Tooltip.of(Text.literal(candidate.route() + " · capital $" + FlipNotifier.format(candidate.acquisitionCost())
                            + (candidate.route().equals("ORDER_TO_AUCTION") ? " · list $" + FlipNotifier.format(candidate.targetListPrice()) : "")
                            + " · slots O/A/I " + candidate.orderSlots() + "/" + candidate.auctionSlots() + "/" + candidate.inventorySlots()
                            + " · queue #" + candidate.queuePosition() + " · " + candidate.orderTier()))).dimensions(left, top + 92 + index * 22, WIDTH - 94, 20).build());
            ButtonWidget arm = ButtonWidget.builder(Text.literal("Arm 1"), button -> client.setScreen(new OrderArmScreen(this, orderExecutor, candidate)))
                    .tooltip(Tooltip.of(Text.literal("Review and explicitly arm one order creation."))).dimensions(left + WIDTH - 90, top + 92 + index * 22, 90, 20).build();
            arm.active = candidate.route().equals("ORDER_TO_AUCTION") && !orderExecutor.status().active();
            addDrawableChild(arm);
        }

        List<FlipFeedClient.Flip> flips = auctions.flips();
        for (int index = 0; index < Math.min(3, flips.size()); index++) {
            FlipFeedClient.Flip flip = flips.get(index);
            String label = abbreviate(flip.itemName(), 25) + "  $" + FlipNotifier.format(flip.price()) + "  +$" + FlipNotifier.format(flip.profit());
            addDrawableChild(ButtonWidget.builder(Text.literal(label), button -> FlipNotifier.open(client, flip))
                    .tooltip(Tooltip.of(Text.literal("API auction · seller " + flip.seller() + " · confidence " + flip.confidenceBps() / 100.0 + "%")))
                    .dimensions(left, top + 228 + index * 22, WIDTH, 20).build());
        }

        addDrawableChild(ButtonWidget.builder(Text.literal("Chat alerts: " + (auctions.alertsEnabled() ? "ON" : "OFF")), button -> { auctions.setAlertsEnabled(!auctions.alertsEnabled()); clearAndInit(); })
                .dimensions(left, top + 300, 144, 20).build());
        addDrawableChild(ButtonWidget.builder(Text.literal("Diagnostics: " + (candidates.diagnosticsEnabled() ? "ON" : "OFF")), button -> { candidates.setDiagnostics(!candidates.diagnosticsEnabled()); clearAndInit(); })
                .dimensions(left + 148, top + 300, 144, 20).build());
        if (orderExecutor.status().active()) {
            addDrawableChild(ButtonWidget.builder(Text.literal("STOP ORDER"), button -> { orderExecutor.cancel(client, "cancelled by player"); clearAndInit(); })
                    .dimensions(left + 296, top + 300, 144, 20).build());
        } else addDrawableChild(ButtonWidget.builder(Text.literal("Close"), button -> close()).dimensions(left + 296, top + 300, 144, 20).build());
        renderedKey = feedKey();
    }

    @Override public void tick() { super.tick(); if (!renderedKey.equals(feedKey())) clearAndInit(); }

    @Override public void render(DrawContext context, int mouseX, int mouseY, float delta) {
        context.fill(0, 0, width, height, 0xFF101010);
        int left = (width - WIDTH) / 2; int top = Math.max(12, (height - 350) / 2);
        context.drawCenteredTextWithShadow(textRenderer, title, width / 2, top, 0xFFFFFF);
        PortfolioAllocator.Allocation portfolio = candidates.allocation();
        context.drawTextWithShadow(textRenderer, Text.literal("Balance $" + FlipNotifier.format(portfolio.balance()) + " (" + candidates.balanceSource() + ") · deployable $"
                + FlipNotifier.format(portfolio.deployable()) + " · reserve " + portfolio.reserveBps() / 100.0 + "%"), left, top + 20, 0xDDDDDD);
        context.drawTextWithShadow(textRenderer, Text.literal("Orders: " + candidates.status().state() + " · READY " + stateCount("READY")
                + " HOLD " + stateCount("HOLD") + " STALE " + stateCount("STALE") + " RESEARCH " + stateCount("RESEARCH")
                + " · API " + auctions.status().state() + "/" + auctions.status().flipCount()), left, top + 32, 0xAAAAAA);
        if (orderExecutor.status().phase() != OrderCreationExecutor.Phase.IDLE) {
            context.drawTextWithShadow(textRenderer, Text.literal("Executor: " + orderExecutor.status().phase() + " · " + orderExecutor.status().message()), left, top + 66, 0xFFCC66);
        }
        context.drawTextWithShadow(textRenderer, Text.literal("Selected order portfolio"), left, top + 77, 0xFFFFFF);
        if (portfolio.selections().isEmpty()) context.drawTextWithShadow(textRenderer, Text.literal("No READY order candidates fit the local portfolio."), left, top + 105, 0x888888);
        context.drawTextWithShadow(textRenderer, Text.literal("API auction opportunities"), left, top + 213, 0xFFFFFF);
        if (auctions.flips().isEmpty()) context.drawTextWithShadow(textRenderer, Text.literal("No qualified API auction flips right now."), left, top + 242, 0x888888);
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
    private static String abbreviate(String value, int limit) { return value.length() <= limit ? value : value.substring(0, limit - 1) + "…"; }
}

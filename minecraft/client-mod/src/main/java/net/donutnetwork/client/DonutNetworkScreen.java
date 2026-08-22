package net.donutnetwork.client;

import net.minecraft.client.gui.DrawContext;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.client.gui.tooltip.Tooltip;
import net.minecraft.client.gui.widget.ButtonWidget;
import net.minecraft.text.Text;

import java.time.Duration;
import java.time.Instant;
import java.util.List;

/** Intentionally plain operator screen: status, alert toggle, and the best current flips. */
final class DonutNetworkScreen extends Screen {
    private static final int PANEL_WIDTH = 360;
    private static final int ROW_WIDTH = 320;
    private final Screen parent;
    private final BackendSnapshotClient snapshots;
    private final BackendOpportunityClient opportunities;
    private String renderedFeedKey = "";

    DonutNetworkScreen(Screen parent, BackendSnapshotClient snapshots,
                       BackendOpportunityClient opportunities) {
        super(Text.literal("Donut Network"));
        this.parent = parent;
        this.snapshots = snapshots;
        this.opportunities = opportunities;
    }

    @Override
    protected void init() {
        int left = (width - ROW_WIDTH) / 2;
        int top = Math.max(42, (height - 250) / 2);
        addDrawableChild(ButtonWidget.builder(alertButtonText(), button -> {
            opportunities.setAlertsEnabled(!opportunities.alertsEnabled());
            button.setMessage(alertButtonText());
        }).dimensions(left, top + 50, ROW_WIDTH, 20).build());

        List<BackendOpportunityClient.Opportunity> feed = opportunities.opportunities();
        renderedFeedKey = feedKey(feed);
        int visible = Math.min(6, feed.size());
        for (int index = 0; index < visible; index++) {
            BackendOpportunityClient.Opportunity opportunity = feed.get(index);
            String label = abbreviate(opportunity.itemName(), 24)
                    + "  $" + FlipChatNotifier.format(opportunity.price())
                    + "  +$" + FlipChatNotifier.format(opportunity.profit());
            addDrawableChild(ButtonWidget.builder(Text.literal(label),
                    button -> FlipChatNotifier.openAuction(client, opportunity))
                    .tooltip(Tooltip.of(Text.literal("Seller: " + opportunity.seller()
                            + " | Confidence: " + opportunity.confidenceBps() / 100.0 + "%"
                            + " | 24h volume: " + opportunity.volume24h())))
                    .dimensions(left, top + 84 + index * 22, ROW_WIDTH, 20).build());
        }
        addDrawableChild(ButtonWidget.builder(Text.literal("Close"), button -> close())
                .dimensions(left, top + 224, ROW_WIDTH, 20).build());
    }

    @Override
    public void tick() {
        super.tick();
        if (!renderedFeedKey.equals(feedKey(opportunities.opportunities()))) {
            clearAndInit();
        }
    }

    @Override
    public void render(DrawContext context, int mouseX, int mouseY, float delta) {
        renderBackground(context, mouseX, mouseY, delta);
        int panelLeft = (width - PANEL_WIDTH) / 2;
        int top = Math.max(42, (height - 250) / 2);
        context.fill(panelLeft, top - 18, panelLeft + PANEL_WIDTH, top + 250, 0xE0101010);
        context.drawCenteredTextWithShadow(textRenderer, title, width / 2, top - 8, 0xFFFFFF);

        BackendSnapshotClient.Status snapshotStatus = snapshots.status();
        BackendOpportunityClient.Status alertStatus = opportunities.status();
        context.drawTextWithShadow(textRenderer,
                Text.literal("Backend: " + alertStatus.state() + "  |  Models: " + snapshotStatus.values()),
                panelLeft + 20, top + 14, statusColor(alertStatus.state()));
        context.drawTextWithShadow(textRenderer,
                Text.literal("Last feed: " + age(alertStatus.lastSuccess()) + "  |  Good flips: "
                        + alertStatus.opportunities()), panelLeft + 20, top + 28, 0xB8B8B8);
        context.drawTextWithShadow(textRenderer, Text.literal("Click a row to open a filtered /ah search."),
                panelLeft + 20, top + 72, 0x8F8F8F);
        if (opportunities.opportunities().isEmpty()) {
            context.drawCenteredTextWithShadow(textRenderer, "No qualified flips right now.",
                    width / 2, top + 116, 0xA0A0A0);
        }
        super.render(context, mouseX, mouseY, delta);
    }

    @Override
    public void close() {
        client.setScreen(parent);
    }

    @Override
    public boolean shouldPause() {
        return false;
    }

    private Text alertButtonText() {
        return Text.literal("Chat alerts: " + (opportunities.alertsEnabled() ? "ON" : "OFF"));
    }

    private static int statusColor(String state) {
        return "ready".equals(state) ? 0x55FF55 : "error".equals(state) ? 0xFF5555 : 0xFFFF55;
    }

    private static String age(Instant instant) {
        if (instant == null || instant.equals(Instant.EPOCH)) {
            return "never";
        }
        long seconds = Math.max(0, Duration.between(instant, Instant.now()).toSeconds());
        return seconds < 60 ? seconds + "s ago" : seconds / 60 + "m ago";
    }

    private static String feedKey(List<BackendOpportunityClient.Opportunity> feed) {
        if (feed.isEmpty()) {
            return "empty";
        }
        StringBuilder key = new StringBuilder();
        for (int i = 0; i < Math.min(6, feed.size()); i++) {
            key.append(feed.get(i).key()).append('\0');
        }
        return key.toString();
    }

    private static String abbreviate(String value, int maximum) {
        return value.length() <= maximum ? value : value.substring(0, maximum - 1) + "…";
    }
}

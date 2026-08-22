package net.donutnetwork.client;

import net.minecraft.client.gui.DrawContext;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.client.gui.tooltip.Tooltip;
import net.minecraft.client.gui.widget.ButtonWidget;
import net.minecraft.text.Text;

import java.time.Duration;
import java.time.Instant;
import java.util.List;

/** Deliberately plain status/flip screen. It never invokes Minecraft's blur background. */
final class DonutScreen extends Screen {
    private static final int WIDTH = 340;
    private final Screen parent;
    private final FlipFeedClient feed;
    private String renderedKey = "";

    DonutScreen(Screen parent, FlipFeedClient feed) {
        super(Text.literal("Donut auction flips"));
        this.parent = parent;
        this.feed = feed;
    }

    @Override protected void init() {
        int left = (width - WIDTH) / 2;
        int top = Math.max(28, (height - 230) / 2);
        addDrawableChild(ButtonWidget.builder(alertText(), button -> {
            feed.setAlertsEnabled(!feed.alertsEnabled());
            button.setMessage(alertText());
        }).dimensions(left, top + 44, WIDTH, 20).build());
        List<FlipFeedClient.Flip> flips = feed.flips();
        renderedKey = feedKey(flips);
        for (int index = 0; index < Math.min(6, flips.size()); index++) {
            FlipFeedClient.Flip flip = flips.get(index);
            String label = abbreviate(flip.itemName(), 21) + "  $" + FlipNotifier.format(flip.price())
                    + "  +$" + FlipNotifier.format(flip.profit());
            addDrawableChild(ButtonWidget.builder(Text.literal(label), button -> FlipNotifier.open(client, flip))
                    .tooltip(Tooltip.of(Text.literal("Seller " + flip.seller() + " · confidence "
                            + flip.confidenceBps() / 100.0 + "% · " + flip.volume24h() + " sales/24h")))
                    .dimensions(left, top + 76 + index * 22, WIDTH, 20).build());
        }
        addDrawableChild(ButtonWidget.builder(Text.literal("Close"), button -> close())
                .dimensions(left, top + 210, WIDTH, 20).build());
    }

    @Override public void tick() {
        super.tick();
        if (!renderedKey.equals(feedKey(feed.flips()))) {
            clearAndInit();
        }
    }

    @Override public void render(DrawContext context, int mouseX, int mouseY, float delta) {
        context.fill(0, 0, width, height, 0xFF101010);
        int left = (width - WIDTH) / 2;
        int top = Math.max(28, (height - 230) / 2);
        context.drawCenteredTextWithShadow(textRenderer, title, width / 2, top, 0xFFFFFF);
        FlipFeedClient.Status status = feed.status();
        context.drawTextWithShadow(textRenderer, Text.literal("Backend: " + status.state()), left, top + 20,
                "ready".equals(status.state()) ? 0x55FF55 : "error".equals(status.state()) ? 0xFF5555 : 0xFFFF55);
        context.drawTextWithShadow(textRenderer, Text.literal("Last update: " + age(status.lastSuccess())
                + " · flips: " + status.flipCount()), left + 110, top + 20, 0xB8B8B8);
        if (feed.flips().isEmpty()) {
            context.drawCenteredTextWithShadow(textRenderer, "No qualified flips right now.", width / 2, top + 104, 0x999999);
        }
        super.render(context, mouseX, mouseY, delta);
    }

    @Override public void close() { client.setScreen(parent); }
    @Override public boolean shouldPause() { return false; }

    private Text alertText() { return Text.literal("Chat alerts: " + (feed.alertsEnabled() ? "ON" : "OFF")); }

    private static String feedKey(List<FlipFeedClient.Flip> values) {
        StringBuilder key = new StringBuilder();
        for (int index = 0; index < Math.min(6, values.size()); index++) key.append(values.get(index).key()).append('\0');
        return key.toString();
    }

    private static String age(Instant value) {
        if (value == null || value.equals(Instant.EPOCH)) return "never";
        long seconds = Math.max(0, Duration.between(value, Instant.now()).toSeconds());
        return seconds < 60 ? seconds + "s" : seconds / 60 + "m";
    }

    private static String abbreviate(String value, int limit) {
        return value.length() <= limit ? value : value.substring(0, limit - 1) + "…";
    }
}

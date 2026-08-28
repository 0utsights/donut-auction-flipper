package net.donutnetwork.client;

import net.minecraft.client.MinecraftClient;
import net.minecraft.client.gui.Click;
import net.minecraft.client.gui.DrawContext;
import net.minecraft.client.gui.screen.narration.NarrationMessageBuilder;
import net.minecraft.client.gui.tooltip.Tooltip;
import net.minecraft.client.gui.widget.ClickableWidget;
import net.minecraft.client.input.KeyInput;
import net.minecraft.text.Text;

/** Shared restrained blue/black presentation for every Donut market screen. */
final class MarketUi {
    static final int ACCENT = 0xFF4D8DFF;
    static final int ACCENT_BRIGHT = 0xFF75ADFF;
    static final int TEXT = 0xFFF4F7FF;
    static final int MUTED = 0xFF91A0B8;
    static final int GOOD = 0xFF59D499;
    static final int WARN = 0xFFFFC45C;
    static final int BAD = 0xFFFF6B7A;
    static final int PANEL = 0xF20A101B;
    static final int CARD = 0xE8111B2B;
    static final int CARD_HOVER = 0xF0192942;
    static final int BORDER = 0xAA274466;

    enum ButtonStyle { PRIMARY, SECONDARY, DANGER, GHOST }

    private MarketUi() { }

    static int panelWidth(int screenWidth, int preferred) {
        return Math.max(300, Math.min(preferred, screenWidth - 20));
    }

    static int panelHeight(int screenHeight, int preferred) {
        return Math.max(220, Math.min(preferred, screenHeight - 16));
    }

    static int panelLeft(int screenWidth, int panelWidth) { return (screenWidth - panelWidth) / 2; }
    static int panelTop(int screenHeight, int panelHeight) { return (screenHeight - panelHeight) / 2; }

    static void drawBackdrop(DrawContext context, int width, int height) {
        context.fillGradient(0, 0, width, height, 0xF8050910, 0xFA070D18);
        context.fill(0, height / 3, width, height / 3 + 1, 0x332D6CB8);
        context.fill(width / 7, 0, width / 7 + 1, height, 0x162C65A8);
        context.fill(width * 6 / 7, 0, width * 6 / 7 + 1, height, 0x122C65A8);
    }

    static void drawPanel(DrawContext context, int left, int top, int width, int height,
                          String eyebrow, String title, String status, int statusColor) {
        context.fill(left - 3, top - 3, left + width + 3, top + height + 3, 0x55000000);
        context.fill(left, top, left + width, top + height, PANEL);
        context.drawStrokedRectangle(left, top, width, height, BORDER);
        context.fill(left + 1, top + 1, left + width - 1, top + 45, 0xEC0D1726);
        context.fill(left + 1, top + 44, left + width - 1, top + 45, 0xAA3169AD);
        context.fill(left + 16, top + 44, left + 116, top + 46, ACCENT);

        MinecraftClient client = MinecraftClient.getInstance();
        context.drawTextWithShadow(client.textRenderer, Text.literal(eyebrow), left + 16, top + 9, ACCENT_BRIGHT);
        context.drawTextWithShadow(client.textRenderer, Text.literal(title), left + 16, top + 23, TEXT);
        int statusWidth = client.textRenderer.getWidth(status);
        context.drawTextWithShadow(client.textRenderer, Text.literal(status), left + width - 16 - statusWidth, top + 17, statusColor);
    }

    static void drawCard(DrawContext context, int left, int top, int width, int height) {
        context.fill(left, top, left + width, top + height, CARD);
        context.drawStrokedRectangle(left, top, width, height, BORDER);
        context.fill(left + 1, top + 1, left + width - 1, top + 2, 0x193B72B7);
    }

    static MarketButton button(String label, int x, int y, int width, int height,
                               ButtonStyle style, Runnable action, String tooltip) {
        MarketButton button = new MarketButton(x, y, width, height, Text.literal(label), style, action);
        if (tooltip != null && !tooltip.isBlank()) button.setTooltip(Tooltip.of(Text.literal(tooltip)));
        return button;
    }

    static String trim(String value, int length) {
        if (value == null) return "";
        return value.length() <= length ? value : value.substring(0, Math.max(1, length - 1)) + "…";
    }

    static final class MarketButton extends ClickableWidget {
        private final ButtonStyle style;
        private final Runnable action;

        private MarketButton(int x, int y, int width, int height, Text message, ButtonStyle style, Runnable action) {
            super(x, y, width, height, message);
            this.style = style;
            this.action = action;
        }

        @Override protected void renderWidget(DrawContext context, int mouseX, int mouseY, float delta) {
            int background;
            int border;
            int foreground;
            if (!active) {
                background = 0xAA111722; border = 0x66405268; foreground = 0xFF67748A;
            } else if (style == ButtonStyle.PRIMARY) {
                background = hovered ? 0xFF347CE8 : 0xFF255FB8; border = hovered ? ACCENT_BRIGHT : ACCENT; foreground = TEXT;
            } else if (style == ButtonStyle.DANGER) {
                background = hovered ? 0xFFD84858 : 0xFF8F2C39; border = BAD; foreground = TEXT;
            } else if (style == ButtonStyle.GHOST) {
                background = hovered ? 0xD91B2940 : 0x66111B2B; border = hovered ? 0xAA4D8DFF : 0x66274466; foreground = hovered ? TEXT : MUTED;
            } else {
                background = hovered ? CARD_HOVER : CARD; border = hovered ? ACCENT : BORDER; foreground = hovered ? TEXT : 0xFFD8E3F4;
            }
            context.fill(getX(), getY(), getRight(), getBottom(), background);
            context.drawStrokedRectangle(getX(), getY(), width, height, border);
            context.drawCenteredTextWithShadow(MinecraftClient.getInstance().textRenderer, message,
                    getX() + width / 2, getY() + (height - 8) / 2, foreground);
        }

        @Override public void onClick(Click click, boolean doubled) {
            if (active) action.run();
        }

        @Override public boolean keyPressed(KeyInput input) {
            if (!active || !isFocused() || !input.isEnterOrSpace()) return false;
            playDownSound(MinecraftClient.getInstance().getSoundManager());
            action.run();
            return true;
        }

        @Override protected void appendClickableNarrations(NarrationMessageBuilder builder) {
            appendDefaultNarrations(builder);
        }
    }
}

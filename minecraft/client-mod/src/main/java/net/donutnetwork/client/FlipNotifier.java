package net.donutnetwork.client;

import net.minecraft.client.MinecraftClient;
import net.minecraft.text.ClickEvent;
import net.minecraft.text.HoverEvent;
import net.minecraft.text.MutableText;
import net.minecraft.text.Text;
import net.minecraft.util.Formatting;

import java.util.Locale;

final class FlipNotifier {
    private FlipNotifier() {}

    static void send(MinecraftClient client, FlipFeedClient.Flip flip) {
        if (client.player == null) {
            return;
        }
        MutableText message = Text.literal("[DN] ").formatted(Formatting.GOLD, Formatting.BOLD)
                .append(Text.literal(flip.itemName() + " x" + flip.quantity()).formatted(Formatting.WHITE))
                .append(Text.literal("  $" + format(flip.price())).formatted(Formatting.GRAY))
                .append(Text.literal("  +$" + format(flip.profit()) + " ("
                        + String.format(Locale.ROOT, "%.1f", flip.marginBps() / 100.0) + "%)").formatted(Formatting.GREEN));
        if (!flip.sellerCommand().equals(flip.itemSearchCommand())) {
            message.append(action("  [SELLER]", flip.sellerCommand(), "Open " + flip.seller()
                    + "'s listings; match price $" + format(flip.price()) + "."));
        }
        message.append(action("  [ITEM]", flip.itemSearchCommand(), "Search " + flip.itemId()
                + "; match seller " + flip.seller() + " and price $" + format(flip.price()) + "."));
        client.player.sendMessage(message, false);
    }

    private static MutableText action(String label, String command, String hover) {
        return Text.literal(label).styled(style -> style.withColor(Formatting.AQUA)
                .withBold(true).withUnderline(true)
                .withClickEvent(new ClickEvent.RunCommand(command))
                .withHoverEvent(new HoverEvent.ShowText(Text.literal(hover + " Buying remains manual."))));
    }

    static void open(MinecraftClient client, FlipFeedClient.Flip flip) {
        if (client.getNetworkHandler() == null) {
            return;
        }
        client.setScreen(null);
        client.getNetworkHandler().sendChatCommand(FlipFeedClient.commandWithoutSlash(flip));
    }

    static String format(long value) {
        long absolute = Math.abs(value);
        if (absolute >= 1_000_000_000L) return String.format(Locale.ROOT, "%.2fb", value / 1_000_000_000.0);
        if (absolute >= 1_000_000L) return String.format(Locale.ROOT, "%.2fm", value / 1_000_000.0);
        if (absolute >= 1_000L) return String.format(Locale.ROOT, "%.1fk", value / 1_000.0);
        return Long.toString(value);
    }
}

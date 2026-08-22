package net.donutnetwork.client;

import net.minecraft.client.MinecraftClient;
import net.minecraft.text.ClickEvent;
import net.minecraft.text.HoverEvent;
import net.minecraft.text.MutableText;
import net.minecraft.text.Text;
import net.minecraft.util.Formatting;

import java.util.Locale;

final class FlipChatNotifier {
    private FlipChatNotifier() {}

    static void send(MinecraftClient client, BackendOpportunityClient.Opportunity opportunity) {
        if (client.player == null) {
            return;
        }
        String command = "/" + opportunity.auctionCommand();
        MutableText message = Text.literal("[DN] ").formatted(Formatting.GOLD, Formatting.BOLD)
                .append(Text.literal(title(opportunity.itemName()) + " x" + opportunity.quantity())
                        .formatted(Formatting.WHITE))
                .append(Text.literal("  $" + format(opportunity.price())).formatted(Formatting.GRAY))
                .append(Text.literal("  +$" + format(opportunity.profit()) + " ("
                        + String.format(Locale.ROOT, "%.1f", opportunity.marginBps() / 100.0) + "%)")
                        .formatted(Formatting.GREEN))
                .append(Text.literal("  [OPEN]").styled(style -> style
                        .withColor(Formatting.AQUA)
                        .withBold(true)
                        .withUnderline(true)
                        .withClickEvent(new ClickEvent.RunCommand(command))
                        .withHoverEvent(new HoverEvent.ShowText(Text.literal(
                                "Open /ah search for " + opportunity.itemName() + " from "
                                        + opportunity.seller() + " at $" + format(opportunity.price())
                                        + ". Match the seller and price; purchase remains manual.")))));
        client.player.sendMessage(message, false);
    }

    static void sendParsed(MinecraftClient client, ListingEvaluation evaluation) {
        if (client.player == null) {
            return;
        }
        ParsedListing listing = evaluation.listing();
        String path = listing.baseSignature().startsWith("minecraft:")
                ? listing.baseSignature().substring("minecraft:".length()) : listing.baseSignature();
        String command = path.matches("[a-z0-9_]{1,80}") ? "/ah " + path.replace('_', ' ') : "/ah";
        MutableText message = Text.literal("[DN PARSE] ").formatted(Formatting.YELLOW, Formatting.BOLD)
                .append(Text.literal(title(path.replace('_', ' ')) + " x" + listing.quantity())
                        .formatted(Formatting.WHITE))
                .append(Text.literal("  $" + format(listing.totalPrice())).formatted(Formatting.GRAY))
                .append(Text.literal("  +$" + format(evaluation.expectedProfit())).formatted(Formatting.GREEN))
                .append(Text.literal("  [OPEN]").styled(style -> style
                        .withColor(Formatting.AQUA).withBold(true).withUnderline(true)
                        .withClickEvent(new ClickEvent.RunCommand(command))
                        .withHoverEvent(new HoverEvent.ShowText(Text.literal(
                                "Open filtered auction search. Match " + listing.seller()
                                        + " at $" + format(listing.totalPrice()) + "; purchase remains manual.")))));
        client.player.sendMessage(message, false);
    }

    static void openAuction(MinecraftClient client, BackendOpportunityClient.Opportunity opportunity) {
        if (client.getNetworkHandler() == null) {
            return;
        }
        client.setScreen(null);
        client.getNetworkHandler().sendChatCommand(opportunity.auctionCommand());
    }

    static String format(long value) {
        long absolute = Math.abs(value);
        if (absolute >= 1_000_000_000L) {
            return String.format(Locale.ROOT, "%.2fb", value / 1_000_000_000.0);
        }
        if (absolute >= 1_000_000L) {
            return String.format(Locale.ROOT, "%.2fm", value / 1_000_000.0);
        }
        if (absolute >= 1_000L) {
            return String.format(Locale.ROOT, "%.1fk", value / 1_000.0);
        }
        return Long.toString(value);
    }

    private static String title(String value) {
        StringBuilder result = new StringBuilder(value.length());
        boolean capitalize = true;
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            if (c == ' ') {
                result.append(c);
                capitalize = true;
            } else {
                result.append(capitalize ? Character.toUpperCase(c) : c);
                capitalize = false;
            }
        }
        return result.toString();
    }
}

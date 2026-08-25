package net.donutnetwork.client;

import net.minecraft.client.MinecraftClient;
import net.minecraft.text.ClickEvent;
import net.minecraft.text.HoverEvent;
import net.minecraft.text.MutableText;
import net.minecraft.text.Text;
import net.minecraft.util.Formatting;

import java.util.Locale;

final class CandidateNotifier {
    private CandidateNotifier() {}

    static void send(MinecraftClient client, CandidateFeedClient.Candidate candidate) {
        if (client.player == null) return;
        String route = candidate.route().equals("ORDER_TO_AUCTION") ? "order → auction" : "auction → order";
        MutableText message = Text.literal("[DN] ").formatted(Formatting.GOLD, Formatting.BOLD)
                .append(Text.literal(candidate.itemName() + " x" + candidate.quantity()).formatted(Formatting.WHITE))
                .append(Text.literal("  " + route).formatted(Formatting.GRAY))
                .append(Text.literal("  +$" + FlipNotifier.format(candidate.conservativeProfit()) + " · $"
                        + FlipNotifier.format(candidate.riskAdjustedProfitDay()) + "/day").formatted(Formatting.GREEN))
                .append(action("  [OPEN]", primaryCommand(candidate), preparedValues(candidate)));
        client.player.sendMessage(message, false);
    }

    static void open(MinecraftClient client, CandidateFeedClient feed, CandidateFeedClient.Candidate candidate) {
        if (client.getNetworkHandler() == null) return;
        feed.focus(candidate);
        client.setScreen(null);
        client.getNetworkHandler().sendChatCommand(primaryCommand(candidate).substring(1));
    }

    private static String primaryCommand(CandidateFeedClient.Candidate candidate) {
        String command = candidate.route().equals("AUCTION_TO_ORDER") ? candidate.auctionCommand() : candidate.orderCommand();
        if (!command.equals("/orders") && !command.matches("/ah(?: [a-z0-9_-]{1,64})?")) throw new IllegalArgumentException("unsafe candidate command");
        return command;
    }

    private static String preparedValues(CandidateFeedClient.Candidate candidate) {
        if (candidate.route().equals("ORDER_TO_AUCTION")) {
            return "Create " + candidate.quantity() + " at " + formatCents(candidate.orderUnitRewardCents())
                    + " each, then list the exact batch for $" + FlipNotifier.format(candidate.targetListPrice())
                    + ". Recheck all values; every transaction remains manual.";
        }
        return "Open the relevant market. Recheck item, quantity, and price; every transaction remains manual.";
    }

    private static String formatCents(long cents) {
        return String.format(Locale.ROOT, "$%,d.%02d", cents / 100, cents % 100);
    }

    private static MutableText action(String label, String command, String hover) {
        return Text.literal(label).styled(style -> style.withColor(Formatting.AQUA).withBold(true).withUnderline(true)
                .withClickEvent(new ClickEvent.RunCommand(command))
                .withHoverEvent(new HoverEvent.ShowText(Text.literal(hover))));
    }
}

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

    static void send(MinecraftClient client, PortfolioAllocator.Selection selection) {
        if (client.player == null) return;
        CandidateFeedClient.Candidate candidate = selection.candidate();
        String route = candidate.route().equals("ORDER_TO_AUCTION") ? "order → auction" : "auction → order";
        String readiness = "actionable".equals(candidate.orderTier()) ? "CORE" : "FILLER";
        MutableText message = Text.literal("[DN] ").formatted(Formatting.GOLD, Formatting.BOLD)
                .append(Text.literal(readiness + " · " + candidate.itemName() + " x" + selection.orderQuantity()
                        + " (" + selection.batches() + " exit stacks)").formatted(Formatting.WHITE))
                .append(Text.literal("  " + route).formatted(Formatting.GRAY))
                .append(Text.literal("  +$" + FlipNotifier.format(selection.conservativeProfit()) + " · $"
                        + FlipNotifier.format(selection.riskAdjustedProfitDay()) + "/day").formatted(Formatting.GREEN))
                .append(action("  [OPEN]", primaryCommand(candidate), preparedValues(selection)));
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

    private static String preparedValues(PortfolioAllocator.Selection selection) {
        CandidateFeedClient.Candidate candidate = selection.candidate();
        if (candidate.route().equals("ORDER_TO_AUCTION")) {
            return "Create one order for " + selection.orderQuantity() + " at " + formatCents(candidate.orderUnitRewardCents())
                    + " each. Exit as " + selection.batches() + " separate listings of " + candidate.quantity()
                    + " at $" + FlipNotifier.format(candidate.targetListPrice())
                    + " each; reuse the 18 auction slots as listings sell. Recheck all values.";
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

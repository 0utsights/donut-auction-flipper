package net.donutnetwork.client;

import net.fabricmc.api.ClientModInitializer;
import net.fabricmc.fabric.api.client.command.v2.ClientCommandRegistrationCallback;
import net.fabricmc.fabric.api.client.event.lifecycle.v1.ClientLifecycleEvents;
import net.fabricmc.fabric.api.client.event.lifecycle.v1.ClientTickEvents;
import net.fabricmc.fabric.api.client.message.v1.ClientReceiveMessageEvents;
import net.fabricmc.fabric.api.client.keybinding.v1.KeyBindingHelper;
import net.minecraft.client.MinecraftClient;
import net.minecraft.client.option.KeyBinding;
import net.minecraft.client.util.InputUtil;
import net.minecraft.scoreboard.Scoreboard;
import net.minecraft.scoreboard.ScoreboardDisplaySlot;
import net.minecraft.scoreboard.ScoreboardEntry;
import net.minecraft.scoreboard.ScoreboardObjective;
import net.minecraft.scoreboard.Team;
import net.minecraft.scoreboard.number.StyledNumberFormat;
import net.minecraft.text.Text;
import net.minecraft.util.Identifier;
import org.lwjgl.glfw.GLFW;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.ArrayList;
import java.util.List;

import static net.fabricmc.fabric.api.client.command.v2.ClientCommandManager.literal;

/** Thin player-facing client: consume backend candidates, allocate locally, and open manual market routes. */
public final class DonutNetworkClient implements ClientModInitializer {
    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private FlipFeedClient feed;
	private CandidateFeedClient candidates;
    private OrderCreationExecutor orderExecutor;
    private boolean startupHintShown;
    private int scoreboardPollTicks;
    private long nextSidebarDiagnosticAt;

    @Override public void onInitializeClient() {
        try {
            ClientConfig.Settings settings = ClientConfig.load();
            feed = new FlipFeedClient(settings, flip -> MinecraftClient.getInstance().execute(() ->
                    FlipNotifier.send(MinecraftClient.getInstance(), flip)));
			candidates = new CandidateFeedClient(settings, selection -> MinecraftClient.getInstance().execute(() ->
					CandidateNotifier.send(MinecraftClient.getInstance(), selection)));
            orderExecutor = new OrderCreationExecutor(candidates);
            registerControls();
            feed.start();
			candidates.start();
			ClientReceiveMessageEvents.GAME.register((message, overlay) -> {
				candidates.observeBalance(message.getString());
				orderExecutor.observeServerMessage(MinecraftClient.getInstance(), message.getString());
			});
            ClientLifecycleEvents.CLIENT_STOPPING.register(client -> { candidates.close(); feed.close(); });
            LOGGER.info("Donut market client started; backend={}", settings.backend());
        } catch (RuntimeException error) {
            LOGGER.error("Donut client configuration is invalid: {}", error.getMessage());
            ClientCommandRegistrationCallback.EVENT.register((dispatcher, registryAccess) ->
                    dispatcher.register(literal("dn").executes(context -> {
                        MinecraftClient client = MinecraftClient.getInstance();
                        if (client.player != null) client.player.sendMessage(Text.literal("[DN] Configuration error: " + error.getMessage()), false);
                        return 0;
                    })));
        }
    }

    private void registerControls() {
        KeyBinding.Category category = KeyBinding.Category.create(Identifier.of("donut-network", "controls"));
        KeyBinding open = KeyBindingHelper.registerKeyBinding(new KeyBinding(
                "key.donut-network.open", InputUtil.Type.KEYSYM, GLFW.GLFW_KEY_N, category));
        ClientTickEvents.END_CLIENT_TICK.register(client -> {
            while (open.wasPressed()) openScreen(client);
            orderExecutor.tick(client);
            observeSidebarBalance(client);
            showStartupHint(client);
        });
        ClientCommandRegistrationCallback.EVENT.register((dispatcher, registryAccess) ->
                dispatcher.register(literal("dn").executes(context -> {
                    openScreen(MinecraftClient.getInstance());
                    return 1;
                })));
    }

    private void observeSidebarBalance(MinecraftClient client) {
        if (++scoreboardPollTicks < 20) return;
        scoreboardPollTicks = 0;
        if (client.world == null || client.player == null) return;
        Scoreboard scoreboard = client.world.getScoreboard();
        ScoreboardObjective objective = visibleSidebarObjective(scoreboard, client.player.getNameForScoreboard());
        if (objective == null) return;
        List<String> lines = new ArrayList<>();
        for (ScoreboardEntry entry : scoreboard.getScoreboardEntries(objective)) {
            if (entry.hidden()) continue;
            Text name = entry.name();
            Team team = scoreboard.getScoreHolderTeam(entry.owner());
            if (team != null) name = team.decorateName(name);
            Text score = entry.formatted(objective.getNumberFormatOr(StyledNumberFormat.RED));
            lines.add(name.getString());
            lines.add(composeSidebarRow(name.getString(), score.getString()));
            lines.add(score.getString());
            if (entry.display() != null) lines.add(entry.display().getString());
            lines.add(entry.owner());
        }
        boolean observed = candidates.observeSidebarBalance(lines);
        long now = System.currentTimeMillis();
        if (!observed && now >= nextSidebarDiagnosticAt) {
            nextSidebarDiagnosticAt = now + 30_000;
            LOGGER.info("Visible sidebar balance was not parsed; objective={}; rows={}", objective.getName(),
                    summarizeSidebarRows(lines));
        }
    }

    /** Mirrors InGameHud's team-colored sidebar selection before its default fallback. */
    static ScoreboardObjective visibleSidebarObjective(Scoreboard scoreboard, String playerName) {
        ScoreboardObjective objective = null;
        Team playerTeam = scoreboard.getScoreHolderTeam(playerName);
        if (playerTeam != null) {
            ScoreboardDisplaySlot teamSlot = ScoreboardDisplaySlot.fromFormatting(playerTeam.getColor());
            if (teamSlot != null) objective = scoreboard.getObjectiveForSlot(teamSlot);
        }
        return objective != null ? objective : scoreboard.getObjectiveForSlot(ScoreboardDisplaySlot.SIDEBAR);
    }

    static String composeSidebarRow(String name, String score) {
        return ((name == null ? "" : name) + " " + (score == null ? "" : score)).strip();
    }

    static String summarizeSidebarRows(List<String> lines) {
        StringBuilder value = new StringBuilder();
        for (String line : lines) {
            if (line == null || line.isBlank()) continue;
            String clean = diagnosticCodePoints(line.replace('\r', ' ').replace('\n', ' '));
            if (value.length() > 0) value.append(" | ");
            value.append('[').append(clean.substring(0, Math.min(80, clean.length()))).append(']');
            if (value.length() >= 1_200) break;
        }
        return value.substring(0, Math.min(1_200, value.length()));
    }

    private static String diagnosticCodePoints(String value) {
        StringBuilder escaped = new StringBuilder();
        value.codePoints().forEach(codePoint -> {
            if (codePoint >= 0x20 && codePoint <= 0x7e) {
                escaped.appendCodePoint(codePoint);
            } else {
                escaped.append(String.format("<U+%04X>", codePoint));
            }
        });
        return escaped.toString();
    }

    private void openScreen(MinecraftClient client) {
		client.setScreen(new DonutScreen(client.currentScreen, feed, candidates, orderExecutor));
    }

    private void showStartupHint(MinecraftClient client) {
        if (startupHintShown || client.player == null) return;
        FlipFeedClient.Status status = feed.status();
        if ("waiting".equals(status.state())) return;
        startupHintShown = true;
        if ("error".equals(status.state())) {
            client.player.sendMessage(Text.literal("[DN] Backend unavailable. Start the local backend; this mod will reconnect automatically."), false);
        } else {
			client.player.sendMessage(Text.literal("[DN] Connected. Press N or use /dn for API auctions and the local order portfolio."), false);
        }
    }
}

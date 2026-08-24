package net.donutnetwork.client;

import net.fabricmc.api.ClientModInitializer;
import net.fabricmc.fabric.api.client.command.v2.ClientCommandRegistrationCallback;
import net.fabricmc.fabric.api.client.event.lifecycle.v1.ClientLifecycleEvents;
import net.fabricmc.fabric.api.client.event.lifecycle.v1.ClientTickEvents;
import net.fabricmc.fabric.api.client.keybinding.v1.KeyBindingHelper;
import net.minecraft.client.MinecraftClient;
import net.minecraft.client.option.KeyBinding;
import net.minecraft.client.util.InputUtil;
import net.minecraft.text.Text;
import net.minecraft.util.Identifier;
import org.lwjgl.glfw.GLFW;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import static net.fabricmc.fabric.api.client.command.v2.ClientCommandManager.literal;

/** Thin API-only Fabric client: poll, alert, and open a manual auction search. */
public final class DonutNetworkClient implements ClientModInitializer {
    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private FlipFeedClient feed;
    private boolean startupHintShown;

    @Override public void onInitializeClient() {
        try {
            ClientConfig.Settings settings = ClientConfig.load();
            feed = new FlipFeedClient(settings, flip -> MinecraftClient.getInstance().execute(() ->
                    FlipNotifier.send(MinecraftClient.getInstance(), flip)));
            registerControls();
            feed.start();
            ClientLifecycleEvents.CLIENT_STOPPING.register(client -> feed.close());
            LOGGER.info("Donut API-only client started; backend={}", settings.backend());
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
            showStartupHint(client);
        });
        ClientCommandRegistrationCallback.EVENT.register((dispatcher, registryAccess) ->
                dispatcher.register(literal("dn").executes(context -> {
                    openScreen(MinecraftClient.getInstance());
                    return 1;
                })));
    }

    private void openScreen(MinecraftClient client) {
        client.setScreen(new DonutScreen(client.currentScreen, feed));
    }

    private void showStartupHint(MinecraftClient client) {
        if (startupHintShown || client.player == null) return;
        FlipFeedClient.Status status = feed.status();
        if ("waiting".equals(status.state())) return;
        startupHintShown = true;
        if ("error".equals(status.state())) {
            client.player.sendMessage(Text.literal("[DN] Backend unavailable. Start the local backend; this mod will reconnect automatically."), false);
        } else {
            client.player.sendMessage(Text.literal("[DN] Connected. Press N or use /dn to review auction flips."), false);
        }
    }
}

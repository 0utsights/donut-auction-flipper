package net.donutnetwork.client;

import net.fabricmc.api.ClientModInitializer;
import net.fabricmc.fabric.api.client.command.v2.ClientCommandRegistrationCallback;
import net.fabricmc.fabric.api.client.event.lifecycle.v1.ClientTickEvents;
import net.fabricmc.fabric.api.client.event.lifecycle.v1.ClientLifecycleEvents;
import net.fabricmc.fabric.api.client.keybinding.v1.KeyBindingHelper;
import net.minecraft.client.MinecraftClient;
import net.minecraft.client.option.KeyBinding;
import net.minecraft.client.util.InputUtil;
import net.minecraft.util.Identifier;
import org.lwjgl.glfw.GLFW;

import static net.fabricmc.fabric.api.client.command.v2.ClientCommandManager.literal;

/** Installs auction observation, backend alerts, and user-initiated navigation controls. */
public final class DonutNetworkClient implements ClientModInitializer {
    private static final ClientCore CORE = new ClientCore();
    private BackendSnapshotClient backend;
    private BackendOpportunityClient opportunities;
    @Override public void onInitializeClient() {
        CORE.initialize();
        new FabricAuctionScreenObserver(CORE).register();
        ClientConfiguration.Settings settings = ClientConfiguration.load();
        if (settings != null) {
            backend = new BackendSnapshotClient(CORE.prices(), settings.backend());
            opportunities = new BackendOpportunityClient(settings.backend(), settings.alertInterval(),
                    settings.chatAlerts(), opportunity -> MinecraftClient.getInstance().execute(() ->
                    FlipChatNotifier.send(MinecraftClient.getInstance(), opportunity)));
            backend.start();
            opportunities.start();
            registerControls();
            ClientLifecycleEvents.CLIENT_STOPPING.register(client -> {
                backend.close();
                opportunities.close();
            });
        }
    }

    private void registerControls() {
        KeyBinding.Category category = KeyBinding.Category.create(Identifier.of("donut-network", "controls"));
        KeyBinding open = KeyBindingHelper.registerKeyBinding(new KeyBinding(
                "key.donut-network.open", InputUtil.Type.KEYSYM, GLFW.GLFW_KEY_N, category));
        ClientTickEvents.END_CLIENT_TICK.register(client -> {
            while (open.wasPressed()) {
                openScreen(client);
            }
        });
        ClientCommandRegistrationCallback.EVENT.register((dispatcher, registryAccess) ->
                dispatcher.register(literal("dn").executes(context -> {
                    openScreen(MinecraftClient.getInstance());
                    return 1;
                })));
    }

    private void openScreen(MinecraftClient client) {
        client.setScreen(new DonutNetworkScreen(client.currentScreen, backend, opportunities));
    }
}

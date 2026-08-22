package net.donutnetwork.client;

import net.fabricmc.loader.api.FabricLoader;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.Properties;

final class ClientConfiguration {
    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private static final String DEFAULT_CONTENT = """
            # Donut Network backend. This is not the DonutSMP API key.
            enabled=true
            backend_url=http://127.0.0.1:8080
            auth_token=local-worker-token
            poll_seconds=10
            alert_poll_seconds=2
            chat_alerts=true
            """;

    record Settings(BackendSnapshotClient.Config backend, Duration alertInterval, boolean chatAlerts) {}

    private ClientConfiguration() {}

    static Settings load() {
        Path path = path();
        try {
            if (Files.notExists(path)) {
                Files.createDirectories(path.getParent());
                Files.writeString(path, DEFAULT_CONTENT);
            }
            Properties properties = new Properties();
            try (var reader = Files.newBufferedReader(path)) {
                properties.load(reader);
            }
            if (!Boolean.parseBoolean(properties.getProperty("enabled", "true"))) {
                return null;
            }
            long pollSeconds = Long.parseLong(properties.getProperty("poll_seconds", "10").strip());
            long alertPollSeconds = Long.parseLong(properties.getProperty("alert_poll_seconds", "2").strip());
            BackendSnapshotClient.Config backend = new BackendSnapshotClient.Config(
                    URI.create(properties.getProperty("backend_url", "http://127.0.0.1:8080").strip()),
                    properties.getProperty("auth_token", "local-worker-token").strip(),
                    Duration.ofSeconds(pollSeconds));
            if (alertPollSeconds < 2 || alertPollSeconds > 60) {
                throw new IllegalArgumentException("alert_poll_seconds must be between 2 and 60");
            }
            return new Settings(backend, Duration.ofSeconds(alertPollSeconds),
                    Boolean.parseBoolean(properties.getProperty("chat_alerts", "true")));
        } catch (IOException | IllegalArgumentException error) {
            LOGGER.error("Could not load {}: {}", path, error.getMessage());
            return null;
        }
    }

    static synchronized void saveChatAlerts(boolean enabled) {
        Path path = path();
        try {
            Properties properties = new Properties();
            if (Files.exists(path)) {
                try (var reader = Files.newBufferedReader(path)) {
                    properties.load(reader);
                }
            }
            properties.setProperty("chat_alerts", Boolean.toString(enabled));
            try (var writer = Files.newBufferedWriter(path)) {
                properties.store(writer, "Donut Network client settings");
            }
        } catch (IOException error) {
            LOGGER.error("Could not save {}: {}", path, error.getMessage());
        }
    }

    private static Path path() {
        return FabricLoader.getInstance().getConfigDir().resolve("donut-network-client.properties");
    }
}

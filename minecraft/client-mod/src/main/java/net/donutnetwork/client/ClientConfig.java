package net.donutnetwork.client;

import net.fabricmc.loader.api.FabricLoader;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.time.Duration;
import java.util.Properties;

final class ClientConfig {
    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private static final Path PATH = FabricLoader.getInstance().getConfigDir().resolve("donut-network.properties");

    record Settings(URI backend, String token, Duration pollInterval, boolean chatAlerts) {}

    private ClientConfig() {}

    static Settings load() {
        Properties properties = defaults();
        if (Files.exists(PATH)) {
            try (InputStream input = Files.newInputStream(PATH)) {
                properties.load(input);
            } catch (IOException error) {
                LOGGER.warn("Could not read {}: {}", PATH, safeMessage(error));
            }
        } else {
            save(properties);
        }
        URI backend = URI.create(properties.getProperty("backend_url", "http://127.0.0.1:8080").strip());
        if (!("http".equalsIgnoreCase(backend.getScheme()) || "https".equalsIgnoreCase(backend.getScheme()))
                || backend.getHost() == null || backend.getUserInfo() != null || backend.getQuery() != null
                || backend.getFragment() != null) {
            throw new IllegalArgumentException("backend_url must be an http(s) URL without credentials, query, or fragment");
        }
        int seconds;
        try {
            seconds = Integer.parseInt(properties.getProperty("poll_seconds", "2").strip());
        } catch (NumberFormatException error) {
            throw new IllegalArgumentException("poll_seconds must be a whole number", error);
        }
        if (seconds < 2 || seconds > 60) {
            throw new IllegalArgumentException("poll_seconds must be between 2 and 60");
        }
        String token = properties.getProperty("client_token", "").strip();
        if (!token.isEmpty() && (token.length() < 16 || token.length() > 512
                || token.chars().anyMatch(character -> character < 33 || character > 126))) {
            throw new IllegalArgumentException("client_token must be 16-512 printable ASCII characters without spaces");
        }
        return new Settings(backend, token,
                Duration.ofSeconds(seconds), Boolean.parseBoolean(properties.getProperty("chat_alerts", "true")));
    }

    static void saveChatAlerts(boolean enabled) {
        Properties properties = defaults();
        if (Files.exists(PATH)) {
            try (InputStream input = Files.newInputStream(PATH)) {
                properties.load(input);
            } catch (IOException error) {
                LOGGER.warn("Could not update {}: {}", PATH, safeMessage(error));
                return;
            }
        }
        properties.setProperty("chat_alerts", Boolean.toString(enabled));
        save(properties);
    }

    private static Properties defaults() {
        Properties properties = new Properties();
        properties.setProperty("backend_url", "http://127.0.0.1:8080");
        properties.setProperty("client_token", "");
        properties.setProperty("poll_seconds", "2");
        properties.setProperty("chat_alerts", "true");
        return properties;
    }

    private static void save(Properties properties) {
        try {
            Files.createDirectories(PATH.getParent());
            Path temporary = PATH.resolveSibling(PATH.getFileName() + ".tmp");
            try (OutputStream output = Files.newOutputStream(temporary)) {
                properties.store(output, "Donut auction client. DONUT_API_KEY belongs on the backend, never here.");
            }
            try {
                Files.move(temporary, PATH, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
            } catch (IOException unsupportedAtomicMove) {
                Files.move(temporary, PATH, StandardCopyOption.REPLACE_EXISTING);
            }
        } catch (IOException error) {
            LOGGER.warn("Could not save {}: {}", PATH, safeMessage(error));
        }
    }

    private static String safeMessage(Exception error) {
        String message = error.getMessage() == null ? error.getClass().getSimpleName() : error.getMessage();
        return message.replace('\r', ' ').replace('\n', ' ');
    }
}

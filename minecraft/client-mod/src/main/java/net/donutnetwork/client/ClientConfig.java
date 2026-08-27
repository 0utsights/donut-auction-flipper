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
import java.util.Set;
import java.util.Arrays;
import java.util.Locale;
import java.util.stream.Collectors;
import java.util.UUID;

final class ClientConfig {
    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private static final Path PATH = FabricLoader.getInstance().getConfigDir().resolve("donut-network.properties");

    record Settings(URI backend, String token, Duration pollInterval, boolean chatAlerts,
                    long balance, int usedOrderSlots, int usedAuctionSlots, boolean diagnostics, String installId,
                    long orderSessionBudget, Set<String> orderServerHosts, Set<String> activeOrderItems) {}

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
        int milliseconds;
        try {
            milliseconds = Integer.parseInt(properties.getProperty("poll_millis", "250").strip());
        } catch (NumberFormatException error) {
            throw new IllegalArgumentException("poll_millis must be a whole number", error);
        }
        if (milliseconds < 100 || milliseconds > 5_000) {
            throw new IllegalArgumentException("poll_millis must be between 100 and 5000");
        }
        String token = properties.getProperty("client_token", "").strip();
        if (!token.isEmpty() && (token.length() < 16 || token.length() > 512
                || token.chars().anyMatch(character -> character < 33 || character > 126))) {
            throw new IllegalArgumentException("client_token must be 16-512 printable ASCII characters without spaces");
        }
        long balance = boundedLong(properties, "balance", 10_000_000L, 0, Long.MAX_VALUE);
        long orderSessionBudget = boundedLong(properties, "order_session_budget", 10_000_000L, 1, 100_000_000_000L);
        Set<String> orderServerHosts = Arrays.stream(properties.getProperty("order_server_hosts", "play.donutsmp.net,donutsmp.net").split(","))
                .map(value -> value.strip().toLowerCase(Locale.ROOT)).filter(value -> value.matches("[a-z0-9.-]{1,253}"))
                .collect(Collectors.toUnmodifiableSet());
        if (orderServerHosts.isEmpty()) throw new IllegalArgumentException("order_server_hosts must contain at least one hostname");
        Set<String> activeOrderItems = Arrays.stream(properties.getProperty("active_order_items", "").split(","))
                .map(value -> value.strip().toLowerCase(Locale.ROOT)).filter(value -> value.matches("[a-z0-9_.-]+:[a-z0-9_./-]+"))
                .limit(20).collect(Collectors.toUnmodifiableSet());
        int usedOrderSlots = (int) boundedLong(properties, "used_order_slots", 0, 0, 20);
        int usedAuctionSlots = (int) boundedLong(properties, "used_auction_slots", 0, 0, 18);
        String installId = properties.getProperty("install_id", "").strip();
        if (!installId.matches("[A-Za-z0-9_-]{1,128}")) {
            installId = UUID.randomUUID().toString();
            properties.setProperty("install_id", installId);
            save(properties);
        }
        return new Settings(backend, token, Duration.ofMillis(milliseconds),
                Boolean.parseBoolean(properties.getProperty("chat_alerts", "true")), balance,
                usedOrderSlots, usedAuctionSlots, Boolean.parseBoolean(properties.getProperty("diagnostics", "true")), installId,
                orderSessionBudget, orderServerHosts, activeOrderItems);
    }

    static void saveChatAlerts(boolean enabled) {
		update("chat_alerts", Boolean.toString(enabled));
	}

    static void saveLocalState(long balance, int usedOrderSlots, int usedAuctionSlots, Set<String> activeOrderItems) {
		Properties properties = readProperties();
		properties.setProperty("balance", Long.toString(Math.max(0, balance)));
		properties.setProperty("used_order_slots", Integer.toString(Math.max(0, Math.min(20, usedOrderSlots))));
		properties.setProperty("used_auction_slots", Integer.toString(Math.max(0, Math.min(18, usedAuctionSlots))));
		properties.setProperty("active_order_items", activeOrderItems.stream().sorted().collect(Collectors.joining(",")));
		save(properties);
	}

    static void saveDiagnostics(boolean enabled) { update("diagnostics", Boolean.toString(enabled)); }

    private static void update(String key, String value) {
		Properties properties = readProperties();
		properties.setProperty(key, value);
		save(properties);
	}

    private static Properties readProperties() {
        Properties properties = defaults();
        if (Files.exists(PATH)) {
            try (InputStream input = Files.newInputStream(PATH)) {
                properties.load(input);
            } catch (IOException error) {
                LOGGER.warn("Could not update {}: {}", PATH, safeMessage(error));
				return properties;
            }
        }
		return properties;
    }

    private static Properties defaults() {
        Properties properties = new Properties();
        properties.setProperty("backend_url", "http://127.0.0.1:8080");
        properties.setProperty("client_token", "");
        properties.setProperty("poll_millis", "250");
        properties.setProperty("chat_alerts", "true");
		properties.setProperty("balance", "10000000");
		properties.setProperty("used_order_slots", "0");
		properties.setProperty("used_auction_slots", "0");
		properties.setProperty("diagnostics", "true");
		properties.setProperty("install_id", "");
		properties.setProperty("order_session_budget", "10000000");
		properties.setProperty("order_server_hosts", "play.donutsmp.net,donutsmp.net");
		properties.setProperty("active_order_items", "");
        return properties;
    }

	private static long boundedLong(Properties properties, String key, long fallback, long minimum, long maximum) {
		try {
			long value = Long.parseLong(properties.getProperty(key, Long.toString(fallback)).strip());
			if (value < minimum || value > maximum) throw new IllegalArgumentException(key + " is outside its valid range");
			return value;
		} catch (NumberFormatException error) {
			throw new IllegalArgumentException(key + " must be a whole number", error);
		}
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

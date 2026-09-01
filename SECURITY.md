# Security policy

Please report vulnerabilities through GitHub's private vulnerability reporting for this repository. Do not open a public issue containing credentials, authentication tokens, proxy URLs, account identifiers, raw inventories, NBT, chat, or server-private data.

Supported security fixes target the current `main` branch. The auction-only tag is retained for reproducibility but receives only critical fixes.

The backend API key belongs only on the backend host. Collector Microsoft caches and proxy credentials belong only in permission-restricted collector profiles. Fabric installations receive only their scoped client token.

-- LAN access opt-in (ACCESS.md). When non-zero, the site's TLS cert
-- includes a sslip.io-derived hostname covering the host's primary
-- LAN IPv4 so phones and tablets on the same Wi-Fi can reach the
-- site over HTTPS without per-network DNS configuration.
--
-- NOT NULL with default 0 means existing rows behave identically
-- (LAN access disabled) until the user opts in via the Access tab.
ALTER TABLE sites ADD COLUMN lanEnabled INTEGER NOT NULL DEFAULT 0;

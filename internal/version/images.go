package version

// Pinned Docker images used by Locorum. Centralised so upgrades are atomic
// across the codebase and integration tests pin to the same versions.
const (
	TraefikImage = "traefik:v3.5"
	NginxImage   = "nginx:1.28-alpine"
	ApacheImage  = "httpd:2.4-alpine"
	MailhogImage = "mailhog/mailhog"
	AdminerImage = "adminer:latest"
	AlpineImage  = "alpine:3"

	// Per-site backend images get the user-configurable version suffix appended.
	WodbyPHPImagePrefix = "wodby/php:"
	MySQLImagePrefix    = "mysql:"
	MariaDBImagePrefix  = "mariadb:"
	RedisImagePrefix    = "redis:"
	RedisImageSuffix    = "-alpine"
)

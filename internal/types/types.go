package types

type Site struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Domain    string `json:"domain"`
	FilesDir  string `json:"filesDir"`
	PublicDir string `json:"publicDir"`
	Started   bool   `json:"started"`

	PHPVersion   string `json:"phpVersion"`
	MySQLVersion string `json:"mysqlVersion"`
	RedisVersion string `json:"redisVersion"`
	DBPassword   string `json:"dbPassword"`
	WebServer    string `json:"webServer"` // "nginx" or "apache"
	Multisite    string `json:"multisite"` // "", "subdirectory", or "subdomain"

	// Salts is a JSON-encoded map[string]string of the eight WordPress
	// secret keys (AUTH_KEY, SECURE_AUTH_KEY, …, NONCE_SALT). Generated
	// once at site creation and persisted so wp-config.php regenerates
	// produce a byte-identical file (idempotent writes).
	Salts string `json:"-"`

	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

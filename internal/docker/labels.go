package docker

// Container, network and volume labels used to identify Locorum-owned
// resources. Reading these is preferred over name-prefix matching: labels
// survive renames, are visible in `docker inspect`, and let us scope
// cleanup precisely without false positives from unrelated user containers
// that happen to share a prefix.
const (
	LabelPlatform = "io.locorum.platform"
	LabelSite     = "io.locorum.site"
	LabelRole     = "io.locorum.role"
	LabelVersion  = "io.locorum.version"

	// PlatformValue is the constant value carried on every Locorum-owned
	// resource by LabelPlatform. Filtering by this label is the canonical
	// way to enumerate things we created.
	PlatformValue = "locorum"
)

// Container/network role names. Stable strings — written to live container
// labels and used for filtering during cleanup, so changing a value is a
// breaking change.
const (
	RoleRouter   = "router"
	RoleWeb      = "web"
	RolePHP      = "php"
	RoleDatabase = "database"
	RoleRedis    = "redis"
	RoleMail     = "mail"
	RoleAdminer  = "adminer"

	RoleGlobalNetwork = "global-network"
	RoleSiteNetwork   = "site-network"
	RoleDatabaseData  = "database-data"
)

// PlatformLabels returns the label set applied to every Locorum-managed
// Docker resource. Pass site == "" for resources that are not tied to a
// specific site (e.g., the global router).
func PlatformLabels(role, site, appVersion string) map[string]string {
	labels := map[string]string{
		LabelPlatform: PlatformValue,
		LabelRole:     role,
		LabelVersion:  appVersion,
	}
	if site != "" {
		labels[LabelSite] = site
	}
	return labels
}

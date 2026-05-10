package docker

// Container, network and volume labels used to identify Locorum-owned
// resources. Reading these is preferred over name-prefix matching: labels
// survive renames, are visible in `docker inspect`, and let us scope
// cleanup precisely without false positives from unrelated user containers
// that happen to share a prefix.
const (
	LabelPlatform   = "io.locorum.platform"
	LabelSite       = "io.locorum.site"
	LabelRole       = "io.locorum.role"
	LabelVersion    = "io.locorum.version"
	LabelConfigHash = "io.locorum.confighash"

	// PlatformValue is the constant value carried on every Locorum-owned
	// resource by LabelPlatform. Filtering by this label is the canonical
	// way to enumerate things we created.
	PlatformValue = "locorum"
)

// Role is the string identifier for a containerised component. Used both
// as the value of LabelRole on live resources and to look up per-role
// resource caps in roleResources.
type Role = string

// Container/network role names. Stable strings — written to live container
// labels and used for filtering during cleanup, so changing a value is a
// breaking change.
const (
	RoleRouter   Role = "router"
	RoleWeb      Role = "web"
	RolePHP      Role = "php"
	RoleDatabase Role = "database"
	RoleRedis    Role = "redis"
	RoleMail     Role = "mail"
	RoleAdminer  Role = "adminer"

	RoleGlobalNetwork Role = "global-network"
	RoleSiteNetwork   Role = "site-network"
	RoleDatabaseData  Role = "database-data"

	// RoleDefault is the sentinel passed to roleResources when no
	// per-role override applies. resourceDefaults() uses it.
	RoleDefault Role = ""
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

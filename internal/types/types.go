package types

type Site struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Domain   string `json:"domain"`
	FilesDir string `json:"filesDir"`
	Started  bool   `json:"started"`

	PHPVersion   string `json:"phpVersion"`
	MySQLVersion string `json:"mysqlVersion"`
	RedisVersion string `json:"redisVersion"`

	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type Type struct {
}

func NewType() *Type {
	return &Type{}
}

package types

type Site struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Domain  string `json:"domain"`
	Started bool   `json:"started"`
}

type Type struct {
}

func NewType() *Type {
	return &Type{}
}

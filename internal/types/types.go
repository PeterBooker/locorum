package types

type Site struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Domain string `json:"domain"`
}

type Type struct {
}

func NewType() *Type {
	return &Type{}
}

package toon

type FetchErrorStep int

const (
	// TODO : Add errors
	FetchErrorUnknown FetchErrorStep = iota + 1
)

type FetchError struct {
	Message string         `json:"message"`
	Step    FetchErrorStep `json:"step"`
}

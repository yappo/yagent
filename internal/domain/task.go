package domain

import "context"

type TaskDefinition struct {
	ID           string
	Description  string
	Command      string
	Args         []string
	Cwd          string
	Risk         string
	AllowNetwork bool
	Timeout      int
}

type TaskCatalog interface {
	List(context.Context) []TaskDefinition
	Get(context.Context, string) (TaskDefinition, bool)
}

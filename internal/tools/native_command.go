package tools

type ExecCommand struct {
	Cmd             string
	Workdir         string
	MaxOutputTokens int
}

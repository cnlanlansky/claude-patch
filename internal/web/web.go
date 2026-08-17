package web

import (
	_ "embed"
	"html"
	"strings"
)

//go:embed index.html
var template string

type Paths struct {
	Executable string
	Command    string
	Config     string
}

func Render(paths Paths) string {
	replacer := strings.NewReplacer(
		"{{EXE}}", html.EscapeString(paths.Executable),
		"{{CMD}}", html.EscapeString(paths.Command),
		"{{CONFIG}}", html.EscapeString(paths.Config),
	)
	return replacer.Replace(template)
}

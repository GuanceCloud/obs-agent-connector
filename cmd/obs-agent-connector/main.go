package main

import (
	"fmt"
	"os"

	codebuddyhook "github.com/GuanceCloud/obs-agent-connector/internal/adapters/codebuddy/hook"
	codexhook "github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/hook"
	"github.com/GuanceCloud/obs-agent-connector/internal/app"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "hook" {
		switch os.Args[2] {
		case "codebuddy":
			os.Exit(codebuddyhook.RunCLI(os.Args[3:]))
		case "codex":
			os.Exit(codexhook.RunCLI())
		}
	}
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

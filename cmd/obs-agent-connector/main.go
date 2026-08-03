package main

import (
	"fmt"
	"os"

	claudehook "github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/hook"
	codexhook "github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/hook"
	"github.com/GuanceCloud/obs-agent-connector/internal/app"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "hook" {
		switch os.Args[2] {
		case "claude":
			os.Exit(claudehook.RunCLI())
		case "codex":
			os.Exit(codexhook.RunCLI())
		}
	}
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

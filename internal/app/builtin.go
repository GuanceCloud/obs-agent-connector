package app

import (
	"fmt"
	"strings"

	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	telemetryinstall "github.com/GuanceCloud/obs-agent-connector/internal/install"
)

func installBuiltinAdapter(p agent.Definition, input installInput, noConfig bool) error {
	executable, err := currentExecutable()
	if err != nil {
		return fmt.Errorf("resolve connector executable: %w", err)
	}
	options := telemetryinstall.CodexOptions{
		SourceExecutable:      executable,
		DestinationExecutable: executable,
		Endpoint:              input.Endpoint,
		TracePath:             input.TracePath,
		MetricsPath:           input.MetricsPath,
		InstallType:           fixedType,
		XToken:                input.XToken,
		Headers:               append([]string{}, input.Headers...),
		ResourceAttributes:    builtinResourceAttributes(input),
		CaptureContent:        input.CaptureContent,
		MaxChars:              input.MaxChars,
		Enabled:               input.Enabled,
		NoConfig:              noConfig,
	}

	printSingleDetail("Runtime", executable)
	switch p.Name {
	case "claude":
		_, err = telemetryinstall.InstallClaude(telemetryinstall.ClaudeOptions{
			SourceExecutable:      options.SourceExecutable,
			DestinationExecutable: options.DestinationExecutable,
			Endpoint:              options.Endpoint,
			TracePath:             options.TracePath,
			MetricsPath:           options.MetricsPath,
			InstallType:           options.InstallType,
			XToken:                options.XToken,
			Headers:               options.Headers,
			ResourceAttributes:    options.ResourceAttributes,
			CaptureContent:        options.CaptureContent,
			MaxChars:              options.MaxChars,
			Enabled:               options.Enabled,
			NoConfig:              options.NoConfig,
		})
		if err == nil {
			printSingleDetail("Note", "Restart Claude Code to load the reconciled Hook.")
		}
	case "codex":
		var result telemetryinstall.CodexResult
		result, err = telemetryinstall.InstallCodex(options)
		if err == nil && result.TrustSkipped {
			printSingleDetail("Note", "Codex Hook trust was skipped; restart Codex and trust the Hook when prompted.")
		}
	default:
		return fmt.Errorf("%s does not have a built-in telemetry adapter", p.Name)
	}
	if err != nil {
		return fmt.Errorf("install built-in %s adapter: %w", p.Name, err)
	}
	return nil
}

func removeBuiltinAdapter(p agent.Definition, purgeConfig, purgeState bool) error {
	result, err := telemetryinstall.RemoveAdapter(p.Name, "", telemetryinstall.RemoveOptions{
		PurgeConfig: purgeConfig,
		PurgeState:  purgeState,
	})
	if err != nil {
		return err
	}
	printSingleDetail("Hook", removedOrKept(result.HookRemoved))
	if p.Name == "codex" {
		trustStatus := "not found"
		if result.TrustRemoved {
			trustStatus = "removed"
		}
		printSingleDetail("Hook Trust", trustStatus)
	}
	printSingleDetail("Config", removedOrKept(result.ConfigRemoved))
	if purgeState {
		printSingleDetail("State", removedOrKept(result.StatePurged))
	}
	return nil
}

func removedOrKept(removed bool) string {
	if removed {
		return "removed"
	}
	return "kept"
}

func builtinResourceAttributes(input installInput) []string {
	values := append([]string{}, input.GlobalTags...)
	if strings.TrimSpace(input.AgentID) != "" {
		values = append(values, "agent_id="+input.AgentID)
	}
	if strings.TrimSpace(input.AgentName) != "" {
		values = append(values, "agent_name="+input.AgentName)
	}
	return values
}

func installedPluginVersion(p agent.Definition) string {
	if p.IsBuiltin() {
		return version
	}
	return agent.InstalledVersion(p)
}
